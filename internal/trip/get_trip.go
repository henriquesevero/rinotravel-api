package trip

import (
	"context"

	"rinotravel-api/internal/user"
)

type GetTrip struct {
	trips Repository
}

func NewGetTrip(trips Repository) *GetTrip {
	return &GetTrip{trips: trips}
}

func (g *GetTrip) Execute(ctx context.Context, id ID, actor user.ID) (View, error) {
	t, role, err := loadAsMember(ctx, g.trips, id, actor)
	if err != nil {
		return View{}, err
	}
	if err := checkAction(role, ActionRead); err != nil {
		return View{}, err
	}
	return View{Trip: t, Role: role}, nil
}
