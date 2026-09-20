package trip

import (
	"context"
	"errors"
	"fmt"

	"rinotravel-api/internal/user"
)

type TripReader interface {
	FindByID(ctx context.Context, id ID) (Trip, error)
}

// Access is what a caller learns from being authorized on a trip: their role and the trip
// itself, which carries the defaults (time zone, currency, dates) that child entities need.
type Access struct {
	Role Role
	Trip Trip
}

// Authorizer is the single gate other domains use before touching trip content. A caller who is
// not a member gets the same 404 as a missing trip, so trips cannot be discovered.
type Authorizer struct {
	trips TripReader
}

func NewAuthorizer(trips TripReader) *Authorizer {
	return &Authorizer{trips: trips}
}

func (a *Authorizer) Authorize(ctx context.Context, tripID ID, actor user.ID, action Action) (Access, error) {
	t, err := a.trips.FindByID(ctx, tripID)
	if errors.Is(err, ErrNotFound) {
		return Access{}, errTripNotFound()
	}
	if err != nil {
		return Access{}, fmt.Errorf("find trip: %w", err)
	}
	role, ok := t.RoleOf(actor)
	if !ok {
		return Access{}, errTripNotFound()
	}
	if err := checkAction(role, action); err != nil {
		return Access{}, err
	}
	return Access{Role: role, Trip: t}, nil
}
