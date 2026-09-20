// Package resourcetest holds an in-memory resource.Repo with the same version and tombstone
// semantics as the MongoDB one, for use-case tests.
package resourcetest

import (
	"context"
	"sort"
	"sync"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
)

type entry[T any] struct {
	entity T
	seq    int64
}

type Repo[T any] struct {
	mu     sync.Mutex
	base   func(*T) *kernel.Base
	items  map[string]entry[T]
	seqs   map[string]int64
	unique func(existing, candidate T) bool
}

func New[T any](base func(*T) *kernel.Base) *Repo[T] {
	return &Repo[T]{base: base, items: map[string]entry[T]{}, seqs: map[string]int64{}}
}

// WithUnique makes Insert and Replace fail with ErrAlreadyExists when clash reports a conflict
// between a live stored entity and the candidate (the in-memory analogue of a unique index).
func (r *Repo[T]) WithUnique(clash func(existing, candidate T) bool) *Repo[T] {
	r.unique = clash
	return r
}

func (r *Repo[T]) key(tripID, id string) string { return tripID + "/" + id }

func (r *Repo[T]) nextSeq(tripID string) int64 {
	r.seqs[tripID]++
	return r.seqs[tripID]
}

func (r *Repo[T]) clashes(candidate T) bool {
	if r.unique == nil {
		return false
	}
	c := r.base(&candidate)
	for _, e := range r.items {
		other := e.entity
		b := r.base(&other)
		if b.TripID == c.TripID && b.ID != c.ID && !b.IsDeleted() && r.unique(other, candidate) {
			return true
		}
	}
	return false
}

func (r *Repo[T]) Insert(_ context.Context, entity T) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.base(&entity)
	if _, exists := r.items[r.key(b.TripID, b.ID)]; exists || r.clashes(entity) {
		return kernel.ErrAlreadyExists
	}
	r.items[r.key(b.TripID, b.ID)] = entry[T]{entity: entity, seq: r.nextSeq(b.TripID)}
	return nil
}

func (r *Repo[T]) Replace(_ context.Context, entity T) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.base(&entity)
	stored, ok := r.items[r.key(b.TripID, b.ID)]
	storedBase := r.base(&stored.entity)
	if !ok || storedBase.IsDeleted() {
		return kernel.ErrNotFound
	}
	if storedBase.Version != b.Version-1 {
		return kernel.ErrVersionConflict
	}
	if !b.IsDeleted() && r.clashes(entity) {
		return kernel.ErrAlreadyExists
	}
	r.items[r.key(b.TripID, b.ID)] = entry[T]{entity: entity, seq: r.nextSeq(b.TripID)}
	return nil
}

func (r *Repo[T]) Get(ctx context.Context, tripID, id string) (T, error) {
	entity, err := r.GetAny(ctx, tripID, id)
	if err != nil {
		return entity, err
	}
	if r.base(&entity).IsDeleted() {
		var zero T
		return zero, kernel.ErrNotFound
	}
	return entity, nil
}

func (r *Repo[T]) GetAny(_ context.Context, tripID, id string) (T, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.items[r.key(tripID, id)]
	if !ok {
		var zero T
		return zero, kernel.ErrNotFound
	}
	return stored.entity, nil
}

func (r *Repo[T]) List(_ context.Context, tripID string) ([]T, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []T
	for _, e := range r.items {
		entity := e.entity
		b := r.base(&entity)
		if b.TripID == tripID && !b.IsDeleted() {
			out = append(out, entity)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		bi, bj := r.base(&out[i]), r.base(&out[j])
		if !bi.CreatedAt.Equal(bj.CreatedAt) {
			return bi.CreatedAt.Before(bj.CreatedAt)
		}
		return bi.ID < bj.ID
	})
	return out, nil
}

func (r *Repo[T]) Changes(_ context.Context, tripID string, afterSeq int64, limit int) ([]resource.Change[T], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []resource.Change[T]
	for _, e := range r.items {
		entity := e.entity
		b := r.base(&entity)
		if b.TripID != tripID || e.seq <= afterSeq {
			continue
		}
		if afterSeq == 0 && b.IsDeleted() {
			continue
		}
		out = append(out, resource.Change[T]{Seq: e.seq, Entity: entity})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
