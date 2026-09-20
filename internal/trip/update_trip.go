package trip

import (
	"context"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/user"
)

type UpdateTrip struct {
	trips Repository
}

type UpdateTripInput struct {
	TripID      ID
	ActorID     user.ID
	BaseVersion int64
	Patch       DetailsPatch
}

func NewUpdateTrip(trips Repository) *UpdateTrip {
	return &UpdateTrip{trips: trips}
}

func (u *UpdateTrip) Execute(ctx context.Context, in UpdateTripInput) (View, error) {
	t, role, err := loadAsMember(ctx, u.trips, in.TripID, in.ActorID)
	if err != nil {
		return View{}, err
	}
	if err := checkAction(role, ActionUpdateTrip); err != nil {
		return View{}, err
	}
	if in.Patch.IsEmpty() {
		return View{}, apperror.Unprocessable("empty_patch", "At least one field must be provided.")
	}
	if t.Version != in.BaseVersion {
		return View{}, errVersionConflict()
	}

	details, err := t.Apply(in.Patch)
	if err != nil {
		return View{}, err
	}
	if details == t.Details {
		return View{Trip: t, Role: role}, nil
	}

	t.UpdateDetails(details, clock.Now())
	if err := save(ctx, u.trips, t); err != nil {
		return View{}, err
	}
	return View{Trip: t, Role: role}, nil
}
