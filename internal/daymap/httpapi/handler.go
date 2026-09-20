package httpapi

import (
	"encoding/base64"
	"log/slog"
	"net/http"

	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/daymap"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/trip"
)

type Handler struct {
	logger  *slog.Logger
	guard   authapi.Guard
	service *daymap.Service
	limiter *httpx.RateLimiter
}

func New(logger *slog.Logger, guard authapi.Guard, service *daymap.Service, limiter *httpx.RateLimiter) *Handler {
	return &Handler{logger: logger, guard: guard, service: service, limiter: limiter}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	limited := h.limiter.Wrap(h.plan)
	mux.Handle("POST /api/v1/trips/{tripId}/maps/day", httpx.Handle(h.logger, h.guard.Require(limited)))
}

type legResponse struct {
	From            int    `json:"from"`
	To              int    `json:"to"`
	Available       bool   `json:"available"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
	DistanceMeters  int    `json:"distanceMeters,omitempty"`
	Polyline        string `json:"polyline,omitempty"`
}

type response struct {
	Legs []legResponse `json:"legs"`
	// Image is a data URI, present only when the request asked for one and it could be drawn.
	Image string `json:"image,omitempty"`
}

func (h *Handler) plan(w http.ResponseWriter, r *http.Request) error {
	tripID, err := httpres.PathID(r, "tripId")
	if err != nil {
		return err
	}
	var in daymap.Request
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	result, err := h.service.Plan(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), in)
	if err != nil {
		return err
	}

	out := response{Legs: make([]legResponse, 0, len(result.Legs))}
	for _, leg := range result.Legs {
		out.Legs = append(out.Legs, legResponse{
			From: leg.From, To: leg.To, Available: leg.Available,
			DurationSeconds: leg.DurationSeconds, DistanceMeters: leg.DistanceMeters, Polyline: leg.Polyline,
		})
	}
	if result.Image != nil {
		out.Image = "data:" + result.Image.ContentType + ";base64," + base64.StdEncoding.EncodeToString(result.Image.Data)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
