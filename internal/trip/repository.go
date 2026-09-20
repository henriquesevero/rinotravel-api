package trip

import (
	"context"
	"errors"
	"fmt"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/user"
)

type Repository interface {
	Create(ctx context.Context, t Trip) error
	FindByID(ctx context.Context, id ID) (Trip, error)
	ListByMember(ctx context.Context, userID user.ID) ([]Trip, error)
	// Update persists t only if the stored version is t.Version-1, so callers
	// mutate a loaded Trip and pass it back to detect concurrent changes.
	Update(ctx context.Context, t Trip) error
}

type Users interface {
	FindByEmail(ctx context.Context, email string) (user.User, error)
	FindByIDs(ctx context.Context, ids []user.ID) ([]user.User, error)
}

type View struct {
	Trip Trip
	Role Role
}

type MemberView struct {
	Member       Member
	User         user.User
	Capabilities MemberCapabilities
}

func loadAsMember(ctx context.Context, trips Repository, id ID, actor user.ID) (Trip, Role, error) {
	t, err := trips.FindByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return Trip{}, "", errTripNotFound()
	}
	if err != nil {
		return Trip{}, "", fmt.Errorf("find trip: %w", err)
	}
	role, ok := t.RoleOf(actor)
	if !ok {
		return Trip{}, "", errTripNotFound()
	}
	return t, role, nil
}

func save(ctx context.Context, trips Repository, t Trip) error {
	err := trips.Update(ctx, t)
	switch {
	case errors.Is(err, ErrNotFound):
		return errTripNotFound()
	case errors.Is(err, ErrVersionConflict):
		return errVersionConflict()
	case err != nil:
		return fmt.Errorf("update trip: %w", err)
	}
	return nil
}

func errVersionConflict() *apperror.Error {
	return apperror.Conflict("version_conflict", "The trip was modified by someone else. Reload it and try again.")
}
