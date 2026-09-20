package triptest

import (
	"context"
	"slices"
	"sort"
	"sync"

	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type Repository struct {
	mu    sync.Mutex
	trips map[trip.ID]trip.Trip
}

func NewRepository() *Repository {
	return &Repository{trips: make(map[trip.ID]trip.Trip)}
}

func (r *Repository) Create(_ context.Context, t trip.Trip) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.trips[t.ID]; exists {
		return trip.ErrAlreadyExists
	}
	r.trips[t.ID] = clone(t)
	return nil
}

func (r *Repository) FindByID(_ context.Context, id trip.ID) (trip.Trip, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.trips[id]
	if !ok || t.DeletedAt != nil {
		return trip.Trip{}, trip.ErrNotFound
	}
	return clone(t), nil
}

func (r *Repository) ListByMember(_ context.Context, userID user.ID) ([]trip.Trip, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var found []trip.Trip
	for _, t := range r.trips {
		if _, ok := t.RoleOf(userID); ok && t.DeletedAt == nil {
			found = append(found, clone(t))
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].StartDate > found[j].StartDate })
	return found, nil
}

func (r *Repository) Update(_ context.Context, t trip.Trip) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, ok := r.trips[t.ID]
	if !ok || stored.DeletedAt != nil {
		return trip.ErrNotFound
	}
	if stored.Version != t.Version-1 {
		return trip.ErrVersionConflict
	}
	r.trips[t.ID] = clone(t)
	return nil
}

func (r *Repository) Stored(id trip.ID) (trip.Trip, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.trips[id]
	return clone(t), ok
}

func clone(t trip.Trip) trip.Trip {
	t.Members = slices.Clone(t.Members)
	if t.DeletedAt != nil {
		deletedAt := *t.DeletedAt
		t.DeletedAt = &deletedAt
	}
	return t
}
