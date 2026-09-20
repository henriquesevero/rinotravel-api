package httpapi

import (
	"log/slog"
	"net/http"

	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/transfer"
	"rinotravel-api/internal/trip"
)

type Deps struct {
	Logger    *slog.Logger
	Guard     authapi.Guard
	Transfers *transfer.Transfers
	// Planner is optional: without a route provider the plan endpoint is not mounted.
	Planner *transfer.Planner
	// Maps is optional like Planner: without a map service the map endpoint is not mounted.
	Maps       *transfer.Maps
	MapLimiter *httpx.RateLimiter
}

type Handler struct {
	deps   Deps
	Routes httpres.Routes[transfer.Transfer, transfer.Create, transfer.Patch]
}

func New(d Deps) *Handler {
	return &Handler{deps: d, Routes: httpres.Routes[transfer.Transfer, transfer.Create, transfer.Patch]{
		Logger: d.Logger, Guard: d.Guard, Path: "/api/v1/trips/{tripId}/transfers",
		Service: d.Transfers.Resource(), Create: d.Transfers.Create, Update: d.Transfers.Update,
		Present: func(t transfer.Transfer, _ trip.Role) any { return presentTransfer(t) },
	}}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	h.Routes.Mount(mux)
	if h.deps.Planner != nil {
		mux.Handle("POST /api/v1/trips/{tripId}/transfers/plan", httpx.Handle(h.deps.Logger, h.deps.Guard.Require(h.plan)))
	}
	if h.deps.Maps != nil {
		limited := h.deps.MapLimiter.Wrap(h.mapImage)
		mux.Handle("POST /api/v1/trips/{tripId}/transfers/map", httpx.Handle(h.deps.Logger, h.deps.Guard.Require(limited)))
	}
}

type draftTransfer struct {
	Origin          *kernel.LocationInput `json:"origin,omitempty"`
	Destination     *kernel.LocationInput `json:"destination,omitempty"`
	RouteProvider   string                `json:"routeProvider"`
	ExternalRouteID string                `json:"externalRouteId,omitempty"`
	Legs            []transfer.LegInput   `json:"legs"`
}

type routeOption struct {
	DurationMinutes int           `json:"durationMinutes"`
	DistanceMeters  int           `json:"distanceMeters"`
	Transfer        draftTransfer `json:"transfer"`
}

// plan asks the route provider for options and answers with drafts shaped exactly like a create
// request, so the client can show them, let the user pick one and POST it unchanged.
func (h *Handler) plan(w http.ResponseWriter, r *http.Request) error {
	tripID, err := httpres.PathID(r, "tripId")
	if err != nil {
		return err
	}
	var in transfer.PlanRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	drafts, err := h.deps.Planner.Plan(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), in)
	if err != nil {
		return err
	}

	routes := make([]routeOption, 0, len(drafts))
	for _, d := range drafts {
		origin, destination := d.Transfer.Origin.Value, d.Transfer.Destination.Value
		draft := draftTransfer{Origin: &origin, Destination: &destination, Legs: *d.Transfer.Legs}
		if d.Transfer.RouteProvider != nil {
			draft.RouteProvider = *d.Transfer.RouteProvider
		}
		if d.Transfer.ExternalRouteID != nil {
			draft.ExternalRouteID = *d.Transfer.ExternalRouteID
		}
		routes = append(routes, routeOption{DurationMinutes: d.DurationMinutes, DistanceMeters: d.DistanceMeters, Transfer: draft})
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		Routes []routeOption `json:"routes"`
	}{Routes: routes})
	return nil
}

func (h *Handler) SyncSources() []syncengine.Source {
	return []syncengine.Source{h.Routes.SyncSource("transfer")}
}

type LegResponse struct {
	Mode                     string                `json:"mode"`
	Origin                   *httpres.LocationDTO  `json:"origin,omitempty"`
	Destination              *httpres.LocationDTO  `json:"destination,omitempty"`
	Departure                *httpres.ZonedTimeDTO `json:"departure,omitempty"`
	Arrival                  *httpres.ZonedTimeDTO `json:"arrival,omitempty"`
	EstimatedDurationMinutes *int                  `json:"estimatedDurationMinutes,omitempty"`
	DurationMinutes          *int                  `json:"durationMinutes,omitempty"`
	Line                     string                `json:"line,omitempty"`
	Direction                string                `json:"direction,omitempty"`
	Stops                    *int                  `json:"stops,omitempty"`
	Instructions             string                `json:"instructions,omitempty"`
	Cost                     *httpres.MoneyDTO     `json:"cost,omitempty"`
}

type TransferResponse struct {
	httpres.Meta
	Origin          *httpres.LocationDTO `json:"origin,omitempty"`
	Destination     *httpres.LocationDTO `json:"destination,omitempty"`
	Status          string               `json:"status"`
	RouteProvider   string               `json:"routeProvider,omitempty"`
	ExternalRouteID string               `json:"externalRouteId,omitempty"`
	Notes           string               `json:"notes,omitempty"`
	Legs            []LegResponse        `json:"legs"`
	// Departure, Arrival, DurationMinutes and TotalCost are derived from the legs.
	Departure       *httpres.ZonedTimeDTO `json:"departure,omitempty"`
	Arrival         *httpres.ZonedTimeDTO `json:"arrival,omitempty"`
	DurationMinutes *int                  `json:"durationMinutes,omitempty"`
	TotalCost       *httpres.MoneyDTO     `json:"totalCost,omitempty"`
}

func presentTransfer(t transfer.Transfer) TransferResponse {
	resp := TransferResponse{
		Meta: httpres.MetaOf(t.Base), Origin: httpres.LocationOf(t.Origin), Destination: httpres.LocationOf(t.Destination),
		Status: string(t.Status), RouteProvider: t.RouteProvider, ExternalRouteID: t.ExternalRouteID, Notes: t.Notes,
		Legs: make([]LegResponse, 0, len(t.Legs)), Departure: httpres.ZonedOf(t.Departure()), Arrival: httpres.ZonedOf(t.Arrival()),
		TotalCost: httpres.MoneyOf(t.TotalCost()),
	}
	for _, l := range t.Legs {
		leg := LegResponse{
			Mode: string(l.Mode), Origin: httpres.LocationOf(l.Origin), Destination: httpres.LocationOf(l.Destination),
			Departure: httpres.ZonedOf(l.Departure), Arrival: httpres.ZonedOf(l.Arrival),
			EstimatedDurationMinutes: l.DurationMinutes, Line: l.Line, Direction: l.Direction, Stops: l.Stops,
			Instructions: l.Instructions, Cost: httpres.MoneyOf(l.Cost),
		}
		if d, ok := l.EffectiveDuration(); ok {
			minutes := int(d.Minutes())
			leg.DurationMinutes = &minutes
		}
		resp.Legs = append(resp.Legs, leg)
	}
	if d, ok := t.Duration(); ok {
		minutes := int(d.Minutes())
		resp.DurationMinutes = &minutes
	}
	return resp
}

// mapImage answers with the picture itself. It is private and short-lived: the picture is Google's
// content, so browsers may keep it briefly for speed but it is never stored on the server.
func (h *Handler) mapImage(w http.ResponseWriter, r *http.Request) error {
	tripID, err := httpres.PathID(r, "tripId")
	if err != nil {
		return err
	}
	var in transfer.MapRequest
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	image, err := h.deps.Maps.Render(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), in)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", image.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image.Data)
	return nil
}
