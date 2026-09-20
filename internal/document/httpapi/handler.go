package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/document"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/trip"
)

type Deps struct {
	Logger    *slog.Logger
	Guard     authapi.Guard
	Documents *document.Documents
}

type Handler struct {
	deps Deps
}

func New(d Deps) *Handler { return &Handler{deps: d} }

func (h *Handler) Mount(mux *http.ServeMux) {
	const base = "/api/v1/trips/{tripId}/documents"
	route := func(pattern string, fn httpx.HandlerFunc) {
		mux.Handle(pattern, httpx.Handle(h.deps.Logger, h.deps.Guard.Require(fn)))
	}
	route("POST "+base, h.init)
	route("GET "+base, h.list)
	route("GET "+base+"/{id}", h.get)
	route("PATCH "+base+"/{id}", h.update)
	route("DELETE "+base+"/{id}", h.remove)
	route("POST "+base+"/{id}/complete", h.complete)
	route("GET "+base+"/{id}/download", h.download)
}

type LinkDTO struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Response struct {
	httpres.Meta
	OwnerID    string   `json:"ownerId"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	FileName   string   `json:"fileName"`
	MimeType   string   `json:"mimeType"`
	Size       int64    `json:"size"`
	Checksum   string   `json:"checksum"`
	Status     string   `json:"status"`
	Visibility string   `json:"visibility"`
	Link       *LinkDTO `json:"link,omitempty"`
}

// Present never includes the storage key. Everything a client needs to decide whether its offline
// copy is current (version, checksum, size, mime type) is here.
func Present(d document.Document) Response {
	resp := Response{
		Meta: httpres.MetaOf(d.Base), OwnerID: string(d.OwnerID), Name: d.Name, Type: string(d.Type),
		FileName: d.FileName, MimeType: d.MimeType, Size: d.Size, Checksum: d.Checksum,
		Status: string(d.Status), Visibility: string(d.Visibility),
	}
	if d.LinkType != "" {
		resp.Link = &LinkDTO{Type: d.LinkType, ID: d.LinkID}
	}
	return resp
}

type SignedRequest struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

func signed(u document.Upload) SignedRequest {
	return SignedRequest{URL: u.URL, Method: u.Method, Headers: u.Headers, ExpiresAt: u.ExpiresAt}
}

func (h *Handler) init(w http.ResponseWriter, r *http.Request) error {
	tripID, err := httpres.PathID(r, "tripId")
	if err != nil {
		return err
	}
	var in document.InitInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	res, err := h.deps.Documents.Init(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), in)
	if err != nil {
		return err
	}
	w.Header().Set("Location", r.URL.Path+"/"+res.Document.ID)
	httpx.WriteJSON(w, http.StatusCreated, struct {
		Document Response      `json:"document"`
		Upload   SignedRequest `json:"upload"`
	}{Present(res.Document), signed(res.Upload)})
	return nil
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) error {
	tripID, id, err := pathIDs(r)
	if err != nil {
		return err
	}
	res, err := h.deps.Documents.Complete(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, Present(res.Entity))
	return nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	tripID, err := httpres.PathID(r, "tripId")
	if err != nil {
		return err
	}
	docs, _, err := h.deps.Documents.List(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID))
	if err != nil {
		return err
	}
	items := make([]Response, 0, len(docs))
	for _, d := range docs {
		items = append(items, Present(d))
	}
	httpx.WriteJSON(w, http.StatusOK, httpres.List[Response]{Items: items})
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	tripID, id, err := pathIDs(r)
	if err != nil {
		return err
	}
	res, err := h.deps.Documents.Get(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, Present(res.Entity))
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	tripID, id, err := pathIDs(r)
	if err != nil {
		return err
	}
	var patch document.Patch
	if err := httpx.DecodeJSON(w, r, &patch); err != nil {
		return err
	}
	res, err := h.deps.Documents.Update(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), id, patch)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, Present(res.Entity))
	return nil
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) error {
	tripID, id, err := pathIDs(r)
	if err != nil {
		return err
	}
	if err := h.deps.Documents.Delete(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), id, nil); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// download answers with JSON instead of a redirect: a redirect would make the client forward its
// Authorization header to the storage, which invalidates the signed URL.
func (h *Handler) download(w http.ResponseWriter, r *http.Request) error {
	tripID, id, err := pathIDs(r)
	if err != nil {
		return err
	}
	res, err := h.deps.Documents.Download(r.Context(), authapi.UserID(r.Context()), trip.ID(tripID), id)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, struct {
		Document Response      `json:"document"`
		Download SignedRequest `json:"download"`
	}{Present(res.Document), signed(res.Request)})
	return nil
}

func pathIDs(r *http.Request) (tripID, id string, err error) {
	if tripID, err = httpres.PathID(r, "tripId"); err != nil {
		return "", "", err
	}
	id, err = httpres.PathID(r, "id")
	return tripID, id, err
}
