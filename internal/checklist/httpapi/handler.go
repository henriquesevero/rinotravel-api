package httpapi

import (
	"log/slog"
	"net/http"

	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/checklist"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/trip"
)

type Deps struct {
	Logger *slog.Logger
	Guard  authapi.Guard
	Items  *checklist.Items
}

type Handler struct {
	routes httpres.Routes[checklist.Item, checklist.ItemCreate, checklist.ItemPatch]
}

func New(d Deps) *Handler {
	return &Handler{
		routes: httpres.Routes[checklist.Item, checklist.ItemCreate, checklist.ItemPatch]{
			Logger: d.Logger, Guard: d.Guard,
			Path:    "/api/v1/trips/{tripId}/checklist-items",
			Service: d.Items.Resource(),
			Create:  d.Items.Create,
			Update:  d.Items.Update,
			Present: func(item checklist.Item, _ trip.Role) any { return present(item) },
		},
	}
}

func (h *Handler) Mount(mux *http.ServeMux) { h.routes.Mount(mux) }

func (h *Handler) SyncSource() syncengine.Source { return h.routes.SyncSource("checklist_item") }

type Response struct {
	httpres.Meta
	Title    string `json:"title"`
	Category string `json:"category"`
	Checked  bool   `json:"checked"`
	Quantity int    `json:"quantity"`
	Notes    string `json:"notes,omitempty"`
}

func present(i checklist.Item) Response {
	return Response{
		Meta: httpres.MetaOf(i.Base), Title: i.Title, Category: string(i.Category),
		Checked: i.Checked, Quantity: i.Quantity, Notes: i.Notes,
	}
}
