package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"

	"rinotravel-api/internal/apperror"
	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/trip"
)

type Handler struct {
	logger *slog.Logger
	guard  authapi.Guard
	engine *syncengine.Engine
}

func New(logger *slog.Logger, guard authapi.Guard, engine *syncengine.Engine) *Handler {
	return &Handler{logger: logger, guard: guard, engine: engine}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	const path = "/api/v1/trips/{tripId}/sync"
	mux.Handle("GET "+path, httpx.Handle(h.logger, h.guard.Require(h.pull)))
	mux.Handle("POST "+path, httpx.Handle(h.logger, h.guard.Require(h.push)))
}

func (h *Handler) pull(w http.ResponseWriter, r *http.Request) error {
	tripID, err := httpres.PathID(r, "tripId")
	if err != nil {
		return err
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 {
			return apperror.BadRequest("invalid_limit", "limit must be a positive number.")
		}
	}
	res, err := h.engine.Pull(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, res)
	return nil
}

type pushRequest struct {
	Mutations []syncengine.Mutation `json:"mutations"`
}

func (h *Handler) push(w http.ResponseWriter, r *http.Request) error {
	tripID, err := httpres.PathID(r, "tripId")
	if err != nil {
		return err
	}
	var req pushRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	results, err := h.engine.Push(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), req.Mutations)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		Results []syncengine.Result `json:"results"`
	}{Results: results})
	return nil
}
