package trip

import (
	"context"
	"fmt"

	"rinotravel-api/internal/user"
)

type ListTrips struct {
	trips Repository
}

func NewListTrips(trips Repository) *ListTrips {
	return &ListTrips{trips: trips}
}

func (l *ListTrips) Execute(ctx context.Context, actor user.ID) ([]View, error) {
	trips, err := l.trips.ListByMember(ctx, actor)
	if err != nil {
		return nil, fmt.Errorf("list trips: %w", err)
	}

	views := make([]View, 0, len(trips))
	for _, t := range trips {
		if role, ok := t.RoleOf(actor); ok {
			views = append(views, View{Trip: t, Role: role})
		}
	}
	return views, nil
}
