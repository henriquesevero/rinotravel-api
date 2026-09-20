package syncengine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

var ErrDuplicateMutation = errors.New("duplicate mutation")

type Engine struct {
	sources map[string]Source
	order   []Source
	authz   resource.Authorizer
	log     MutationLog
	tx      Transactor
	now     func() time.Time
}

func NewEngine(authz resource.Authorizer, log MutationLog, tx Transactor, sources ...Source) *Engine {
	e := &Engine{sources: map[string]Source{}, authz: authz, log: log, tx: tx, now: clock.Now}
	for _, s := range sources {
		e.sources[s.Name()] = s
		e.order = append(e.order, s)
	}
	return e
}

func (e *Engine) Pull(ctx context.Context, actor user.ID, tripID trip.ID, token string, limit int) (PullResult, error) {
	access, err := e.authz.Authorize(ctx, tripID, actor, trip.ActionRead)
	if err != nil {
		return PullResult{}, err
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	now := e.now()

	var after int64
	if token != "" {
		c, err := decodeCursor(token)
		if err != nil {
			return PullResult{}, err
		}
		if now.Sub(time.Unix(c.IssuedAt, 0)) > CursorMaxAge {
			return PullResult{Changes: []Change{}, ResetRequired: true, ServerTime: now}, nil
		}
		after = c.Seq
	}

	var all []Change
	for _, source := range e.order {
		changes, err := source.Changes(ctx, string(tripID), after, limit+1, access.Role)
		if err != nil {
			return PullResult{}, fmt.Errorf("pull %s: %w", source.Name(), err)
		}
		all = append(all, changes...)
	}
	// Each source returned its own first limit+1 changes, so the merged first `limit` are exactly
	// the first `limit` of the whole trip.
	slices.SortFunc(all, func(a, b Change) int { return int(a.Seq - b.Seq) })

	res := PullResult{Changes: all, ServerTime: now}
	if len(all) > limit {
		res.HasMore = true
		res.Changes = all[:limit]
	}
	if res.Changes == nil {
		res.Changes = []Change{}
	}
	next := after
	if n := len(res.Changes); n > 0 {
		next = res.Changes[n-1].Seq
	}
	res.Cursor = encodeCursor(next, now)
	return res, nil
}

func (e *Engine) Push(ctx context.Context, actor user.ID, tripID trip.ID, mutations []Mutation) ([]Result, error) {
	if _, err := e.authz.Authorize(ctx, tripID, actor, trip.ActionRead); err != nil {
		return nil, err
	}
	if len(mutations) > MaxMutations {
		return nil, apperror.Unprocessable("too_many_mutations", "At most "+strconv.Itoa(MaxMutations)+" mutations per request.")
	}

	results := make([]Result, 0, len(mutations))
	for _, m := range mutations {
		result, err := e.applyOne(ctx, actor, tripID, m)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (e *Engine) applyOne(ctx context.Context, actor user.ID, tripID trip.ID, m Mutation) (Result, error) {
	if problem := e.validate(m); problem != nil {
		return rejected(m.MutationID, problem), nil
	}
	source := e.sources[m.Entity]
	hash := hashOf(tripID, m)

	if result, done, err := e.replay(ctx, actor, m, hash); done || err != nil {
		return result, err
	}

	var result Result
	err := e.tx.Do(ctx, func(ctx context.Context) error {
		outcome, err := source.Apply(ctx, actor, tripID, m)
		if err != nil {
			return err
		}
		result = Result{MutationID: m.MutationID, Status: StatusApplied, Version: outcome.Version, Record: outcome.Record}
		return e.log.Save(ctx, e.record(actor, tripID, m, hash, result))
	})

	switch {
	case err == nil:
		return result, nil
	case errors.Is(err, ErrDuplicateMutation):
		// A concurrent retry won the race; its effects committed and ours rolled back.
		res, _, err := e.replay(ctx, actor, m, hash)
		return res, err
	}

	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		return Result{}, err
	}
	result = e.failure(ctx, tripID, actor, source, m, appErr)
	if err := e.log.Save(ctx, e.record(actor, tripID, m, hash, result)); err != nil && !errors.Is(err, ErrDuplicateMutation) {
		return Result{}, err
	}
	return result, nil
}

func (e *Engine) validate(m Mutation) *apperror.Error {
	switch {
	case !ids.IsValid(m.MutationID):
		return apperror.Unprocessable("invalid_mutation", "mutationId must be a lowercase UUID.")
	case e.sources[m.Entity] == nil:
		return apperror.Unprocessable("unknown_entity", "Unknown entity type.")
	case !ids.IsValid(m.EntityID):
		return apperror.Unprocessable("invalid_mutation", "entityId must be a lowercase UUID.")
	case m.Operation != OpCreate && m.Operation != OpUpdate && m.Operation != OpRemove:
		return apperror.Unprocessable("invalid_mutation", "operation must be CREATE, UPDATE or DELETE.")
	case m.Operation == OpUpdate && m.BaseVersion == nil:
		return apperror.Unprocessable("base_version_required", "UPDATE needs the baseVersion the client last saw.")
	}
	return nil
}

func (e *Engine) replay(ctx context.Context, actor user.ID, m Mutation, hash string) (Result, bool, error) {
	stored, found, err := e.log.Find(ctx, string(actor), m.MutationID)
	if err != nil {
		return Result{}, false, fmt.Errorf("find mutation: %w", err)
	}
	if !found {
		return Result{}, false, nil
	}
	if stored.Hash != hash {
		return rejected(m.MutationID, apperror.Unprocessable("mutation_id_reused", "This mutationId was already used for a different mutation.")), true, nil
	}
	result := stored.Result
	if result.Status == StatusApplied {
		result.Status = StatusDuplicate
	}
	return result, true, nil
}

// failure turns a business error into a per-mutation result. A version conflict carries the
// server's current copy so the client can resolve it; nothing was written.
func (e *Engine) failure(ctx context.Context, tripID trip.ID, actor user.ID, source Source, m Mutation, appErr *apperror.Error) Result {
	if appErr.Code == "version_conflict" || (appErr.Kind == apperror.KindNotFound && m.Operation == OpUpdate && appErr.Code != "trip_not_found") {
		result := Result{MutationID: m.MutationID, Status: StatusConflict, Code: appErr.Code, Message: appErr.Message}
		if access, err := e.authz.Authorize(ctx, tripID, actor, trip.ActionRead); err == nil {
			if current, ok, err := source.Current(ctx, string(tripID), m.EntityID, access.Role); err == nil && ok {
				result.Version = current.Version
				if current.Op == OpDelete {
					result.Code = "entity_deleted"
				} else {
					result.Record = current.Record
				}
			}
		}
		return result
	}
	return rejected(m.MutationID, appErr)
}

func rejected(mutationID string, appErr *apperror.Error) Result {
	return Result{MutationID: mutationID, Status: StatusRejected, Code: appErr.Code, Message: appErr.Message}
}

func (e *Engine) record(actor user.ID, tripID trip.ID, m Mutation, hash string, result Result) MutationRecord {
	return MutationRecord{
		UserID: string(actor), MutationID: m.MutationID, TripID: string(tripID),
		Hash: hash, Result: result, CreatedAt: e.now(),
	}
}

// hashOf fingerprints everything that defines a mutation, so a retry with the same mutationId is
// recognised while a reused id with different content is refused.
func hashOf(tripID trip.ID, m Mutation) string {
	var payload bytes.Buffer
	if len(m.Payload) > 0 && json.Compact(&payload, m.Payload) != nil {
		payload.Reset()
		payload.Write(m.Payload)
	}
	base := "-"
	if m.BaseVersion != nil {
		base = strconv.FormatInt(*m.BaseVersion, 10)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%s|%s", tripID, m.Entity, m.EntityID, m.Operation, base, payload.String())))
	return hex.EncodeToString(sum[:])
}
