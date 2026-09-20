package trip

import (
	"context"

	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/user"
)

type TransferOwnership struct {
	trips Repository
}

type TransferOwnershipInput struct {
	TripID   ID
	ActorID  user.ID
	TargetID user.ID
}

func NewTransferOwnership(trips Repository) *TransferOwnership {
	return &TransferOwnership{trips: trips}
}

func (tr *TransferOwnership) Execute(ctx context.Context, in TransferOwnershipInput) (View, error) {
	t, role, err := loadAsMember(ctx, tr.trips, in.TripID, in.ActorID)
	if err != nil {
		return View{}, err
	}
	if err := checkAction(role, ActionTransferOwnership); err != nil {
		return View{}, err
	}

	if err := t.TransferOwnership(in.ActorID, in.TargetID, clock.Now()); err != nil {
		return View{}, err
	}
	if err := save(ctx, tr.trips, t); err != nil {
		return View{}, err
	}
	newRole, _ := t.RoleOf(in.ActorID)
	return View{Trip: t, Role: newRole}, nil
}
