package kernel

import (
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrVersionConflict = errors.New("version conflict")
	ErrAlreadyExists   = errors.New("already exists")
)

// Base is the sync-ready envelope every trip-scoped entity embeds: identity, the trip it belongs to,
// an optimistic-concurrency version and a tombstone marker.
type Base struct {
	ID        string
	TripID    string
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

func NewBase(id, tripID string, now time.Time) Base {
	return Base{ID: id, TripID: tripID, Version: 1, CreatedAt: now, UpdatedAt: now}
}

func (b *Base) Touch(now time.Time) {
	b.Version++
	b.UpdatedAt = now
}

func (b *Base) MarkDeleted(now time.Time) {
	deletedAt := now
	b.DeletedAt = &deletedAt
	b.Touch(now)
}

func (b Base) IsDeleted() bool {
	return b.DeletedAt != nil
}
