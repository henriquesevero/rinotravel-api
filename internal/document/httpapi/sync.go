package httpapi

import (
	"context"
	"errors"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/document"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type source struct {
	docs *document.Documents
}

// SyncSource syncs document metadata only. Files travel through signed URLs, so a document is
// created by the upload flow and never by a CREATE mutation; sync covers edits and deletion.
func (h *Handler) SyncSource() syncengine.Source { return &source{docs: h.deps.Documents} }

func (s *source) Name() string { return "document" }

// change decides what a given user may learn about a document. A private document of someone else
// becomes a delete, so a copy that used to be shared is removed from other devices; unconfirmed
// uploads are not announced at all.
func (s *source) change(d document.Document, seq int64, actor user.ID) (syncengine.Change, bool) {
	c := syncengine.Change{Entity: "document", ID: d.ID, Version: d.Version, Seq: seq}
	switch {
	case d.IsDeleted() || !d.VisibleTo(actor):
		c.Op = syncengine.OpDelete
	case d.Status != document.StatusReady:
		return syncengine.Change{}, false
	default:
		c.Op, c.Record = syncengine.OpUpsert, Present(d)
	}
	return c, true
}

func (s *source) Changes(ctx context.Context, actor user.ID, tripID string, afterSeq int64, limit int, _ trip.Role) ([]syncengine.Change, error) {
	found, err := s.docs.Resource().Repo().Changes(ctx, tripID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	out := make([]syncengine.Change, 0, len(found))
	for _, c := range found {
		if change, ok := s.change(c.Entity, c.Seq, actor); ok {
			out = append(out, change)
		}
	}
	return out, nil
}

func (s *source) Current(ctx context.Context, actor user.ID, tripID, id string, _ trip.Role) (syncengine.Change, bool, error) {
	d, err := s.docs.Resource().Repo().GetAny(ctx, tripID, id)
	if errors.Is(err, kernel.ErrNotFound) {
		return syncengine.Change{}, false, nil
	}
	if err != nil {
		return syncengine.Change{}, false, err
	}
	change, ok := s.change(d, 0, actor)
	return change, ok, nil
}

func (s *source) Apply(ctx context.Context, actor user.ID, tripID trip.ID, m syncengine.Mutation) (syncengine.Outcome, error) {
	switch m.Operation {
	case syncengine.OpUpdate:
		var patch document.Patch
		if err := httpres.DecodePayload(m.Payload, map[string]any{"baseVersion": *m.BaseVersion}, &patch); err != nil {
			return syncengine.Outcome{}, err
		}
		res, err := s.docs.Update(ctx, actor, tripID, m.EntityID, patch)
		if err != nil {
			return syncengine.Outcome{}, err
		}
		return syncengine.Outcome{Version: res.Entity.Version, Record: Present(res.Entity)}, nil

	case syncengine.OpRemove:
		err := s.docs.Delete(ctx, actor, tripID, m.EntityID, m.BaseVersion)
		if err != nil {
			var appErr *apperror.Error
			if errors.As(err, &appErr) && appErr.Kind == apperror.KindNotFound && appErr.Code == "document_not_found" {
				if d, getErr := s.docs.Resource().Repo().GetAny(ctx, string(tripID), m.EntityID); getErr == nil && d.IsDeleted() {
					return syncengine.Outcome{Version: d.Version}, nil
				}
			}
			return syncengine.Outcome{}, err
		}
		d, err := s.docs.Resource().Repo().GetAny(ctx, string(tripID), m.EntityID)
		if err != nil {
			return syncengine.Outcome{}, err
		}
		return syncengine.Outcome{Version: d.Version}, nil
	}
	return syncengine.Outcome{}, apperror.Unprocessable("unsupported_operation", "Documents are created through the upload flow (POST /documents), not through sync.")
}
