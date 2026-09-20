package httpres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type syncSource[T, C, U any] struct {
	name   string
	routes Routes[T, C, U]
}

// SyncSource exposes the entity to the sync engine using the very same use cases as the REST API.
func (r Routes[T, C, U]) SyncSource(name string) syncengine.Source {
	return &syncSource[T, C, U]{name: name, routes: r}
}

func (s *syncSource[T, C, U]) Name() string { return s.name }

func (s *syncSource[T, C, U]) change(entity T, seq int64, role trip.Role) syncengine.Change {
	b := s.routes.Service.Base(&entity)
	if b.IsDeleted() {
		return syncengine.Change{Entity: s.name, ID: b.ID, Op: syncengine.OpDelete, Version: b.Version, Seq: seq}
	}
	return syncengine.Change{Entity: s.name, ID: b.ID, Op: syncengine.OpUpsert, Version: b.Version, Seq: seq, Record: s.routes.Present(entity, role)}
}

func (s *syncSource[T, C, U]) Changes(ctx context.Context, _ user.ID, tripID string, afterSeq int64, limit int, role trip.Role) ([]syncengine.Change, error) {
	found, err := s.routes.Service.Repo().Changes(ctx, tripID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	out := make([]syncengine.Change, 0, len(found))
	for _, c := range found {
		out = append(out, s.change(c.Entity, c.Seq, role))
	}
	return out, nil
}

func (s *syncSource[T, C, U]) Current(ctx context.Context, _ user.ID, tripID, id string, role trip.Role) (syncengine.Change, bool, error) {
	entity, err := s.routes.Service.Repo().GetAny(ctx, tripID, id)
	if errors.Is(err, kernel.ErrNotFound) {
		return syncengine.Change{}, false, nil
	}
	if err != nil {
		return syncengine.Change{}, false, err
	}
	return s.change(entity, 0, role), true, nil
}

func (s *syncSource[T, C, U]) Apply(ctx context.Context, actor user.ID, tripID trip.ID, m syncengine.Mutation) (syncengine.Outcome, error) {
	switch m.Operation {
	case syncengine.OpCreate:
		var in C
		if err := DecodePayload(m.Payload, map[string]any{"id": m.EntityID}, &in); err != nil {
			return syncengine.Outcome{}, err
		}
		res, err := s.routes.Create(ctx, actor, tripID, in)
		return s.outcome(res, err)
	case syncengine.OpUpdate:
		var in U
		if err := DecodePayload(m.Payload, map[string]any{"baseVersion": *m.BaseVersion}, &in); err != nil {
			return syncengine.Outcome{}, err
		}
		res, err := s.routes.Update(ctx, actor, tripID, m.EntityID, in)
		return s.outcome(res, err)
	default:
		return s.remove(ctx, actor, tripID, m)
	}
}

func (s *syncSource[T, C, U]) outcome(res resource.Result[T], err error) (syncengine.Outcome, error) {
	if err != nil {
		return syncengine.Outcome{}, err
	}
	entity := res.Entity
	return syncengine.Outcome{Version: s.routes.Service.Base(&entity).Version, Record: s.routes.Present(res.Entity, res.Role)}, nil
}

// remove is idempotent: deleting something already deleted succeeds, so a retried or replayed
// delete never turns into an error.
func (s *syncSource[T, C, U]) remove(ctx context.Context, actor user.ID, tripID trip.ID, m syncengine.Mutation) (syncengine.Outcome, error) {
	var err error
	if s.routes.Delete != nil {
		err = s.routes.Delete(ctx, actor, tripID, m.EntityID, m.BaseVersion)
	} else {
		err = s.routes.Service.Delete(ctx, actor, tripID, m.EntityID, m.BaseVersion)
	}
	if err == nil {
		return s.tombstone(ctx, tripID, m.EntityID)
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) && appErr.Kind == apperror.KindNotFound && appErr.Code != "trip_not_found" {
		if entity, getErr := s.routes.Service.Repo().GetAny(ctx, string(tripID), m.EntityID); getErr == nil {
			b := s.routes.Service.Base(&entity)
			if b.IsDeleted() {
				return syncengine.Outcome{Version: b.Version}, nil
			}
		}
	}
	return syncengine.Outcome{}, err
}

func (s *syncSource[T, C, U]) tombstone(ctx context.Context, tripID trip.ID, id string) (syncengine.Outcome, error) {
	entity, err := s.routes.Service.Repo().GetAny(ctx, string(tripID), id)
	if err != nil {
		return syncengine.Outcome{}, err
	}
	return syncengine.Outcome{Version: s.routes.Service.Base(&entity).Version}, nil
}

// DecodePayload strictly decodes a mutation payload after forcing server-decided fields (the
// entity id, the base version) into it, so the wire shape matches the REST bodies exactly.
func DecodePayload(raw json.RawMessage, force map[string]any, dst any) error {
	fields := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return apperror.Unprocessable("invalid_payload", "The payload must be a JSON object.")
		}
	}
	for key, value := range force {
		encoded, _ := json.Marshal(value)
		fields[key] = encoded
	}
	body, _ := json.Marshal(fields)

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return apperror.Unprocessable("invalid_payload", "The payload does not match the entity's shape.")
	}
	return nil
}
