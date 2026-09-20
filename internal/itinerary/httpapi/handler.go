package httpapi

import (
	"log/slog"
	"net/http"

	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/itinerary"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/trip"
)

type Deps struct {
	Logger   *slog.Logger
	Guard    authapi.Guard
	Days     *itinerary.Days
	Items    *itinerary.Items
	Timeline *itinerary.Timeline
	Places   itinerary.PlaceReader
}

type Handler struct {
	deps  Deps
	Days  httpres.Routes[itinerary.Day, itinerary.DayCreate, itinerary.DayPatch]
	Items httpres.Routes[itinerary.Item, itinerary.ItemCreate, itinerary.ItemPatch]
}

func New(d Deps) *Handler {
	return &Handler{
		deps: d,
		Days: httpres.Routes[itinerary.Day, itinerary.DayCreate, itinerary.DayPatch]{
			Logger: d.Logger, Guard: d.Guard,
			Path:    "/api/v1/trips/{tripId}/itinerary-days",
			Service: d.Days.Resource(),
			Create:  d.Days.Create,
			Update:  d.Days.Update,
			Delete:  d.Days.Delete,
			Present: func(day itinerary.Day, _ trip.Role) any { return presentDay(day) },
		},
		Items: httpres.Routes[itinerary.Item, itinerary.ItemCreate, itinerary.ItemPatch]{
			Logger: d.Logger, Guard: d.Guard,
			Path:    "/api/v1/trips/{tripId}/itinerary-items",
			Service: d.Items.Resource(),
			Create:  d.Items.Create,
			Update:  d.Items.Update,
			Present: func(item itinerary.Item, _ trip.Role) any { return presentItem(item) },
		},
	}
}

func (h *Handler) SyncSources() []syncengine.Source {
	return []syncengine.Source{h.Days.SyncSource("itinerary_day"), h.Items.SyncSource("itinerary_item")}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	h.Days.Mount(mux)
	h.Items.Mount(mux)
	mux.Handle("GET /api/v1/trips/{tripId}/itinerary", httpx.Handle(h.deps.Logger, h.deps.Guard.Require(h.timeline)))
	mux.Handle("POST /api/v1/trips/{tripId}/itinerary-items/from-place", httpx.Handle(h.deps.Logger, h.deps.Guard.Require(h.fromPlace)))
}

type DayResponse struct {
	httpres.Meta
	Date  string `json:"date"`
	Title string `json:"title,omitempty"`
	Notes string `json:"notes,omitempty"`
}

func presentDay(d itinerary.Day) DayResponse {
	return DayResponse{Meta: httpres.MetaOf(d.Base), Date: string(d.Date), Title: d.Title, Notes: d.Notes}
}

type ItemResponse struct {
	httpres.Meta
	DayID                    string                `json:"dayId"`
	Title                    string                `json:"title"`
	Description              string                `json:"description,omitempty"`
	Category                 string                `json:"category"`
	Status                   string                `json:"status"`
	Start                    *httpres.ZonedTimeDTO `json:"start,omitempty"`
	End                      *httpres.ZonedTimeDTO `json:"end,omitempty"`
	Location                 *httpres.LocationDTO  `json:"location,omitempty"`
	EstimatedDurationMinutes *int                  `json:"estimatedDurationMinutes,omitempty"`
	// DurationMinutes is derived: the span between start and end, else the estimate.
	DurationMinutes *int              `json:"durationMinutes,omitempty"`
	EstimatedCost   *httpres.MoneyDTO `json:"estimatedCost,omitempty"`
	Notes           string            `json:"notes,omitempty"`
	Position        int               `json:"position"`
	PlaceID         string            `json:"placeId,omitempty"`
}

func presentItem(i itinerary.Item) ItemResponse {
	resp := ItemResponse{
		Meta: httpres.MetaOf(i.Base), DayID: i.DayID, Title: i.Title, Description: i.Description,
		Category: string(i.Category), Status: string(i.Status),
		Start: httpres.ZonedOf(i.Start), End: httpres.ZonedOf(i.End), Location: httpres.LocationOf(i.Location),
		EstimatedDurationMinutes: i.DurationMinutes, EstimatedCost: httpres.MoneyOf(i.Cost),
		Notes: i.Notes, Position: i.Position, PlaceID: i.PlaceID,
	}
	if d, ok := i.EffectiveDuration(); ok {
		minutes := int(d.Minutes())
		resp.DurationMinutes = &minutes
	}
	return resp
}

type timelineDay struct {
	Date    string                     `json:"date"`
	Day     *DayResponse               `json:"day"`
	Entries []httpres.TimelineEntryDTO `json:"entries"`
}

func (h *Handler) timeline(w http.ResponseWriter, r *http.Request) error {
	tripID, err := httpres.PathID(r, "tripId")
	if err != nil {
		return err
	}
	got, _, err := h.deps.Timeline.Get(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID))
	if err != nil {
		return err
	}

	days := make([]timelineDay, 0, len(got.Days))
	for _, view := range got.Days {
		entries := make([]httpres.TimelineEntryDTO, 0, len(view.Entries))
		for _, entry := range view.Entries {
			entries = append(entries, httpres.EntryOf(entry))
		}
		out := timelineDay{Date: string(view.Date), Entries: entries}
		if view.Day != nil {
			day := presentDay(*view.Day)
			out.Day = &day
		}
		days = append(days, out)
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		Days []timelineDay `json:"days"`
	}{Days: days})
	return nil
}

// fromPlace turns a wishlist place into an itinerary item on the given day.
func (h *Handler) fromPlace(w http.ResponseWriter, r *http.Request) error {
	tripID, err := httpres.PathID(r, "tripId")
	if err != nil {
		return err
	}
	var in itinerary.ScheduleFromPlace
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.deps.Items.CreateFromPlace(r.Context(), h.deps.Places, authapi.UserID(r.Context()), trip.ID(tripID), in)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, presentItem(res.Entity))
	return nil
}
