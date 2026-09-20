package trip

import (
	"context"

	"rinotravel-api/internal/platform/clock"
	"rinotravel-api/internal/user"
)

type DeleteTrip struct {
	trips Repository
}

func NewDeleteTrip(trips Repository) *DeleteTrip {
	return &DeleteTrip{trips: trips}
}

func (d *DeleteTrip) Execute(ctx context.Context, id ID, actor user.ID) error {
	t, role, err := loadAsMember(ctx, d.trips, id, actor)
	if err != nil {
		return err
	}
	if err := checkAction(role, ActionDeleteTrip); err != nil {
		return err
	}

	t.MarkDeleted(clock.Now())
	return save(ctx, d.trips, t)
}
