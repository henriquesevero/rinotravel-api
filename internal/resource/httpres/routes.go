// Package httpres exposes any resource.Service over the standard REST shape:
// POST/GET on the collection and GET/PATCH/DELETE on one item.
package httpres

import (
	"context"
	"log/slog"
	"net/http"

	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

// Routes wires one entity. C and U are the create and patch bodies; a patch body embeds
// kernel.Versioned so the base version travels with it.
type Routes[T, C, U any] struct {
	Logger *slog.Logger
	Guard  authapi.Guard
	// Path is the collection path, for example /api/v1/trips/{tripId}/itinerary-items.
	Path    string
	Service *resource.Service[T]
	Create  func(ctx context.Context, actor user.ID, tripID trip.ID, in C) (resource.Result[T], error)
	Update  func(ctx context.Context, actor user.ID, tripID trip.ID, id string, in U) (resource.Result[T], error)
	// Delete overrides the default tombstone delete when an entity has extra rules.
	Delete  func(ctx context.Context, actor user.ID, tripID trip.ID, id string, baseVersion *int64) error
	Present func(entity T, role trip.Role) any
}

func (r Routes[T, C, U]) Mount(mux *http.ServeMux) {
	route := func(pattern string, fn httpx.HandlerFunc) {
		mux.Handle(pattern, httpx.Handle(r.Logger, r.Guard.Require(fn)))
	}
	route("POST "+r.Path, r.create)
	route("GET "+r.Path, r.list)
	route("GET "+r.Path+"/{id}", r.get)
	route("PATCH "+r.Path+"/{id}", r.update)
	route("DELETE "+r.Path+"/{id}", r.remove)
}

func (r Routes[T, C, U]) create(w http.ResponseWriter, req *http.Request) error {
	tripID, err := PathID(req, "tripId")
	if err != nil {
		return err
	}
	var in C
	if err := httpx.DecodeJSON(w, req, &in); err != nil {
		return err
	}
	res, err := r.Create(req.Context(), authapi.UserID(req.Context()), trip.ID(tripID), in)
	if err != nil {
		return err
	}
	w.Header().Set("Location", req.URL.Path+"/"+r.idOf(res))
	httpx.WriteJSON(w, http.StatusCreated, r.Present(res.Entity, res.Role))
	return nil
}

func (r Routes[T, C, U]) idOf(res resource.Result[T]) string {
	entity := res.Entity
	return r.Service.Base(&entity).ID
}

func (r Routes[T, C, U]) list(w http.ResponseWriter, req *http.Request) error {
	tripID, err := PathID(req, "tripId")
	if err != nil {
		return err
	}
	entities, role, err := r.Service.List(req.Context(), authapi.UserID(req.Context()), trip.ID(tripID))
	if err != nil {
		return err
	}
	items := make([]any, 0, len(entities))
	for _, entity := range entities {
		items = append(items, r.Present(entity, role))
	}
	httpx.WriteJSON(w, http.StatusOK, List[any]{Items: items})
	return nil
}

func (r Routes[T, C, U]) get(w http.ResponseWriter, req *http.Request) error {
	tripID, id, err := ids2(req)
	if err != nil {
		return err
	}
	res, err := r.Service.Get(req.Context(), authapi.UserID(req.Context()), trip.ID(tripID), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, r.Present(res.Entity, res.Role))
	return nil
}

func (r Routes[T, C, U]) update(w http.ResponseWriter, req *http.Request) error {
	tripID, id, err := ids2(req)
	if err != nil {
		return err
	}
	var in U
	if err := httpx.DecodeJSON(w, req, &in); err != nil {
		return err
	}
	res, err := r.Update(req.Context(), authapi.UserID(req.Context()), trip.ID(tripID), id, in)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, r.Present(res.Entity, res.Role))
	return nil
}

func (r Routes[T, C, U]) remove(w http.ResponseWriter, req *http.Request) error {
	tripID, id, err := ids2(req)
	if err != nil {
		return err
	}
	actor := authapi.UserID(req.Context())
	if r.Delete != nil {
		err = r.Delete(req.Context(), actor, trip.ID(tripID), id, nil)
	} else {
		err = r.Service.Delete(req.Context(), actor, trip.ID(tripID), id, nil)
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func ids2(req *http.Request) (tripID, id string, err error) {
	if tripID, err = PathID(req, "tripId"); err != nil {
		return "", "", err
	}
	if id, err = PathID(req, "id"); err != nil {
		return "", "", err
	}
	return tripID, id, nil
}
