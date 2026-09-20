package httpapi

import (
	"context"
	"errors"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type tripSource struct {
	h    *Handler
	seqs trip.SeqReader
}

// SyncSource lets clients pull the trip record and push edits and deletions. Creating a trip
// stays a plain POST /trips (with a client id), because the sync endpoint needs an existing trip.
// Membership changes bump the version but travel through the members API.
func (h *Handler) SyncSource(seqs trip.SeqReader) syncengine.Source {
	return &tripSource{h: h, seqs: seqs}
}

func (s *tripSource) Name() string { return "trip" }

func (s *tripSource) Changes(ctx context.Context, _ user.ID, tripID string, afterSeq int64, _ int, role trip.Role) ([]syncengine.Change, error) {
	change, seq, err := s.current(ctx, tripID, role)
	if err != nil || change == nil || seq <= afterSeq {
		return nil, err
	}
	change.Seq = seq
	return []syncengine.Change{*change}, nil
}

func (s *tripSource) current(ctx context.Context, tripID string, role trip.Role) (*syncengine.Change, int64, error) {
	t, seq, err := s.seqs.FindWithSeq(ctx, trip.ID(tripID))
	if errors.Is(err, trip.ErrNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	return &syncengine.Change{
		Entity: "trip", ID: string(t.ID), Op: syncengine.OpUpsert, Version: t.Version,
		Record: toTripResponse(trip.View{Trip: t, Role: role}),
	}, seq, nil
}

func (s *tripSource) Current(ctx context.Context, _ user.ID, tripID, id string, role trip.Role) (syncengine.Change, bool, error) {
	if tripID != id {
		return syncengine.Change{}, false, nil
	}
	change, _, err := s.current(ctx, tripID, role)
	if err != nil || change == nil {
		return syncengine.Change{}, false, err
	}
	return *change, true, nil
}

func (s *tripSource) Apply(ctx context.Context, actor user.ID, tripID trip.ID, m syncengine.Mutation) (syncengine.Outcome, error) {
	if string(tripID) != m.EntityID {
		return syncengine.Outcome{}, apperror.Unprocessable("invalid_mutation", "For the trip entity, entityId must be the trip id.")
	}
	d := s.h.deps
	switch m.Operation {
	case syncengine.OpUpdate:
		var req updateTripRequest
		if err := httpres.DecodePayload(m.Payload, map[string]any{"baseVersion": *m.BaseVersion}, &req); err != nil {
			return syncengine.Outcome{}, err
		}
		view, err := d.UpdateTrip.Execute(ctx, trip.UpdateTripInput{
			TripID: tripID, ActorID: actor, BaseVersion: *req.BaseVersion,
			Patch: trip.DetailsPatch{
				Name: req.Name, Destination: req.Destination, StartDate: req.StartDate,
				EndDate: req.EndDate, Timezone: req.Timezone, Currency: req.Currency,
			},
		})
		if err != nil {
			return syncengine.Outcome{}, err
		}
		return syncengine.Outcome{Version: view.Trip.Version, Record: toTripResponse(view)}, nil

	case syncengine.OpRemove:
		view, err := d.GetTrip.Execute(ctx, tripID, actor)
		if err != nil {
			return syncengine.Outcome{}, err
		}
		if m.BaseVersion != nil && view.Trip.Version != *m.BaseVersion {
			return syncengine.Outcome{}, apperror.Conflict("version_conflict", "The trip was modified by someone else. Reload it and try again.")
		}
		if err := d.DeleteTrip.Execute(ctx, tripID, actor); err != nil {
			return syncengine.Outcome{}, err
		}
		return syncengine.Outcome{Version: view.Trip.Version + 1}, nil
	}
	return syncengine.Outcome{}, apperror.Unprocessable("unsupported_operation", "Create trips with POST /api/v1/trips.")
}
