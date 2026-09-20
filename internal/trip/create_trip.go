package trip

import (
	"context"
	"errors"
	"fmt"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/user"
)

type CreateTrip struct {
	trips Repository
}

type CreateTripInput struct {
	ActorID user.ID
	ID      string
	Details DetailsInput
}

func NewCreateTrip(trips Repository) *CreateTrip {
	return &CreateTrip{trips: trips}
}

func (c *CreateTrip) Execute(ctx context.Context, in CreateTripInput) (View, error) {
	id := in.ID
	if id == "" {
		id = ids.New()
	} else if !ids.IsValid(id) {
		return View{}, apperror.Validation(apperror.FieldError{Field: "id", Message: "must be a lowercase UUID"})
	}

	details, err := NewDetails(in.Details)
	if err != nil {
		return View{}, err
	}

	t := New(ID(id), in.ActorID, details, clock.Now())
	if err := c.trips.Create(ctx, t); err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			return View{}, apperror.Conflict("trip_id_taken", "A trip with this id already exists.")
		}
		return View{}, fmt.Errorf("create trip: %w", err)
	}
	return View{Trip: t, Role: RoleOwner}, nil
}
