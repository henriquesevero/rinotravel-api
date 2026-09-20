// Package resource is the shared lifecycle of every trip-scoped, versioned, soft-deletable entity
// (itinerary items, places, flights, ...): authorization, id handling, optimistic concurrency and
// tombstones live here once, while each feature only supplies its own rules.
package resource

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type Change[T any] struct {
	Seq    int64
	Entity T
}

type Repo[T any] interface {
	Insert(ctx context.Context, entity T) error
	// Replace stores entity only if the stored version is entity.Version-1.
	Replace(ctx context.Context, entity T) error
	// Get returns a live entity of the trip; tombstones are kernel.ErrNotFound.
	Get(ctx context.Context, tripID, id string) (T, error)
	// GetAny also returns tombstones.
	GetAny(ctx context.Context, tripID, id string) (T, error)
	List(ctx context.Context, tripID string) ([]T, error)
	// Changes returns entities changed after afterSeq ordered by seq, including tombstones.
	Changes(ctx context.Context, tripID string, afterSeq int64, limit int) ([]Change[T], error)
}

type Authorizer interface {
	Authorize(ctx context.Context, tripID trip.ID, actor user.ID, action trip.Action) (trip.Access, error)
}

type Config[T any] struct {
	// Name is the entity's snake_case name; it prefixes error codes such as "<name>_not_found".
	Name string
	Base func(*T) *kernel.Base
}

type Result[T any] struct {
	Entity T
	Role   trip.Role
}

type Service[T any] struct {
	repo  Repo[T]
	authz Authorizer
	cfg   Config[T]
	now   func() time.Time
}

func NewService[T any](repo Repo[T], authz Authorizer, cfg Config[T]) *Service[T] {
	return &Service[T]{repo: repo, authz: authz, cfg: cfg, now: clock.Now}
}

func (s *Service[T]) Repo() Repo[T]          { return s.repo }
func (s *Service[T]) Name() string           { return s.cfg.Name }
func (s *Service[T]) Base(e *T) *kernel.Base { return s.cfg.Base(e) }

func (s *Service[T]) notFound() *apperror.Error {
	return apperror.NotFound(s.cfg.Name+"_not_found", "The requested resource does not exist.")
}

func (s *Service[T]) versionConflict() *apperror.Error {
	return apperror.Conflict("version_conflict", "This item was modified by someone else. Reload it and try again.")
}

func (s *Service[T]) Create(
	ctx context.Context,
	actor user.ID,
	tripID trip.ID,
	clientID string,
	build func(access trip.Access) (T, error),
) (Result[T], error) {
	access, err := s.authz.Authorize(ctx, tripID, actor, trip.ActionWriteContent)
	if err != nil {
		return Result[T]{}, err
	}

	id := clientID
	switch {
	case id == "":
		id = ids.New()
	case !ids.IsValid(id):
		return Result[T]{}, apperror.Validation(apperror.FieldError{Field: "id", Message: "must be a lowercase UUID"})
	}

	entity, err := build(access)
	if err != nil {
		return Result[T]{}, err
	}
	*s.cfg.Base(&entity) = kernel.NewBase(id, string(tripID), s.now())

	if err := s.repo.Insert(ctx, entity); err != nil {
		if errors.Is(err, kernel.ErrAlreadyExists) {
			return Result[T]{}, apperror.Conflict(s.cfg.Name+"_conflict", "An item with this id or unique key already exists.")
		}
		return Result[T]{}, fmt.Errorf("insert %s: %w", s.cfg.Name, err)
	}
	return Result[T]{Entity: entity, Role: access.Role}, nil
}

// Update applies mutate to the stored entity. mutate returns the new value; when it equals the
// current one nothing is written and the version does not move.
func (s *Service[T]) Update(
	ctx context.Context,
	actor user.ID,
	tripID trip.ID,
	id string,
	baseVersion int64,
	mutate func(current T, access trip.Access) (T, error),
) (Result[T], error) {
	access, err := s.authz.Authorize(ctx, tripID, actor, trip.ActionWriteContent)
	if err != nil {
		return Result[T]{}, err
	}
	current, err := s.repo.Get(ctx, string(tripID), id)
	if errors.Is(err, kernel.ErrNotFound) {
		return Result[T]{}, s.notFound()
	}
	if err != nil {
		return Result[T]{}, fmt.Errorf("get %s: %w", s.cfg.Name, err)
	}
	if s.cfg.Base(&current).Version != baseVersion {
		return Result[T]{}, s.versionConflict()
	}

	next, err := mutate(current, access)
	if err != nil {
		return Result[T]{}, err
	}
	if reflect.DeepEqual(next, current) {
		return Result[T]{Entity: current, Role: access.Role}, nil
	}

	s.cfg.Base(&next).Touch(s.now())
	if err := s.replace(ctx, next); err != nil {
		return Result[T]{}, err
	}
	return Result[T]{Entity: next, Role: access.Role}, nil
}

// Delete writes a tombstone. A nil baseVersion skips the concurrency check.
func (s *Service[T]) Delete(ctx context.Context, actor user.ID, tripID trip.ID, id string, baseVersion *int64) error {
	if _, err := s.authz.Authorize(ctx, tripID, actor, trip.ActionWriteContent); err != nil {
		return err
	}
	current, err := s.repo.Get(ctx, string(tripID), id)
	if errors.Is(err, kernel.ErrNotFound) {
		return s.notFound()
	}
	if err != nil {
		return fmt.Errorf("get %s: %w", s.cfg.Name, err)
	}
	if baseVersion != nil && s.cfg.Base(&current).Version != *baseVersion {
		return s.versionConflict()
	}

	s.cfg.Base(&current).MarkDeleted(s.now())
	return s.replace(ctx, current)
}

func (s *Service[T]) replace(ctx context.Context, entity T) error {
	err := s.repo.Replace(ctx, entity)
	switch {
	case errors.Is(err, kernel.ErrNotFound):
		return s.notFound()
	case errors.Is(err, kernel.ErrVersionConflict):
		return s.versionConflict()
	case errors.Is(err, kernel.ErrAlreadyExists):
		return apperror.Conflict(s.cfg.Name+"_conflict", "An item with this unique key already exists.")
	case err != nil:
		return fmt.Errorf("replace %s: %w", s.cfg.Name, err)
	}
	return nil
}

func (s *Service[T]) Get(ctx context.Context, actor user.ID, tripID trip.ID, id string) (Result[T], error) {
	access, err := s.authz.Authorize(ctx, tripID, actor, trip.ActionRead)
	if err != nil {
		return Result[T]{}, err
	}
	entity, err := s.repo.Get(ctx, string(tripID), id)
	if errors.Is(err, kernel.ErrNotFound) {
		return Result[T]{}, s.notFound()
	}
	if err != nil {
		return Result[T]{}, fmt.Errorf("get %s: %w", s.cfg.Name, err)
	}
	return Result[T]{Entity: entity, Role: access.Role}, nil
}

func (s *Service[T]) List(ctx context.Context, actor user.ID, tripID trip.ID) ([]T, trip.Role, error) {
	access, err := s.authz.Authorize(ctx, tripID, actor, trip.ActionRead)
	if err != nil {
		return nil, "", err
	}
	entities, err := s.repo.List(ctx, string(tripID))
	if err != nil {
		return nil, "", fmt.Errorf("list %s: %w", s.cfg.Name, err)
	}
	return entities, access.Role, nil
}
