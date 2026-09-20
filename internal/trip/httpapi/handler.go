package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"rinotravel-api/internal/apperror"
	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type Deps struct {
	Logger            *slog.Logger
	Guard             authapi.Guard
	CreateTrip        *trip.CreateTrip
	GetTrip           *trip.GetTrip
	ListTrips         *trip.ListTrips
	UpdateTrip        *trip.UpdateTrip
	DeleteTrip        *trip.DeleteTrip
	AddMember         *trip.AddMember
	ListMembers       *trip.ListMembers
	ChangeMemberRole  *trip.ChangeMemberRole
	RemoveMember      *trip.RemoveMember
	TransferOwnership *trip.TransferOwnership
}

type Handler struct {
	deps Deps
}

func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	d := h.deps
	route := func(pattern string, fn httpx.HandlerFunc) {
		mux.Handle(pattern, httpx.Handle(d.Logger, d.Guard.Require(fn)))
	}

	route("GET /api/v1/trips", h.list)
	route("POST /api/v1/trips", h.create)
	route("GET /api/v1/trips/{id}", h.get)
	route("PATCH /api/v1/trips/{id}", h.update)
	route("DELETE /api/v1/trips/{id}", h.delete)
	route("POST /api/v1/trips/{id}/transfer-ownership", h.transferOwnership)
	route("GET /api/v1/trips/{id}/members", h.listMembers)
	route("POST /api/v1/trips/{id}/members", h.addMember)
	route("PATCH /api/v1/trips/{id}/members/{userId}", h.changeMemberRole)
	route("DELETE /api/v1/trips/{id}/members/{userId}", h.removeMember)
}

type tripRequest struct {
	Name        string `json:"name"`
	Destination string `json:"destination"`
	StartDate   string `json:"startDate"`
	EndDate     string `json:"endDate"`
	Timezone    string `json:"timezone"`
	Currency    string `json:"currency"`
}

type createTripRequest struct {
	ID string `json:"id"`
	tripRequest
}

type updateTripRequest struct {
	BaseVersion *int64  `json:"baseVersion"`
	Name        *string `json:"name"`
	Destination *string `json:"destination"`
	StartDate   *string `json:"startDate"`
	EndDate     *string `json:"endDate"`
	Timezone    *string `json:"timezone"`
	Currency    *string `json:"currency"`
}

type addMemberRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type changeRoleRequest struct {
	Role string `json:"role"`
}

type transferRequest struct {
	UserID string `json:"userId"`
}

type tripResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Destination string    `json:"destination"`
	StartDate   string    `json:"startDate"`
	EndDate     string    `json:"endDate"`
	Timezone    string    `json:"timezone"`
	Currency    string    `json:"currency"`
	OwnerID     string    `json:"ownerId"`
	MyRole      string    `json:"myRole"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type memberResponse struct {
	UserID    string    `json:"userId"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type listResponse[T any] struct {
	Items []T `json:"items"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	views, err := h.deps.ListTrips.Execute(r.Context(), authapi.UserID(r.Context()))
	if err != nil {
		return err
	}

	items := make([]tripResponse, 0, len(views))
	for _, v := range views {
		items = append(items, toTripResponse(v))
	}
	httpx.WriteJSON(w, http.StatusOK, listResponse[tripResponse]{Items: items})
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var req createTripRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}

	view, err := h.deps.CreateTrip.Execute(r.Context(), trip.CreateTripInput{
		ActorID: authapi.UserID(r.Context()),
		ID:      req.ID,
		Details: trip.DetailsInput{
			Name:        req.Name,
			Destination: req.Destination,
			StartDate:   req.StartDate,
			EndDate:     req.EndDate,
			Timezone:    req.Timezone,
			Currency:    req.Currency,
		},
	})
	if err != nil {
		return err
	}
	w.Header().Set("Location", "/api/v1/trips/"+string(view.Trip.ID))
	httpx.WriteJSON(w, http.StatusCreated, toTripResponse(view))
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	view, err := h.deps.GetTrip.Execute(r.Context(), trip.ID(id), authapi.UserID(r.Context()))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toTripResponse(view))
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req updateTripRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if req.BaseVersion == nil {
		return apperror.Validation(apperror.FieldError{Field: "baseVersion", Message: "is required"})
	}

	view, err := h.deps.UpdateTrip.Execute(r.Context(), trip.UpdateTripInput{
		TripID:      trip.ID(id),
		ActorID:     authapi.UserID(r.Context()),
		BaseVersion: *req.BaseVersion,
		Patch: trip.DetailsPatch{
			Name:        req.Name,
			Destination: req.Destination,
			StartDate:   req.StartDate,
			EndDate:     req.EndDate,
			Timezone:    req.Timezone,
			Currency:    req.Currency,
		},
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toTripResponse(view))
	return nil
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.deps.DeleteTrip.Execute(r.Context(), trip.ID(id), authapi.UserID(r.Context())); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) transferOwnership(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req transferRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	if !ids.IsValid(req.UserID) {
		return apperror.Validation(apperror.FieldError{Field: "userId", Message: "must be a lowercase UUID"})
	}

	view, err := h.deps.TransferOwnership.Execute(r.Context(), trip.TransferOwnershipInput{
		TripID:   trip.ID(id),
		ActorID:  authapi.UserID(r.Context()),
		TargetID: user.ID(req.UserID),
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toTripResponse(view))
	return nil
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	views, err := h.deps.ListMembers.Execute(r.Context(), trip.ID(id), authapi.UserID(r.Context()))
	if err != nil {
		return err
	}

	items := make([]memberResponse, 0, len(views))
	for _, v := range views {
		items = append(items, toMemberResponse(v))
	}
	httpx.WriteJSON(w, http.StatusOK, listResponse[memberResponse]{Items: items})
	return nil
}

func (h *Handler) addMember(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req addMemberRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}

	view, err := h.deps.AddMember.Execute(r.Context(), trip.AddMemberInput{
		TripID:  trip.ID(id),
		ActorID: authapi.UserID(r.Context()),
		Email:   req.Email,
		Role:    req.Role,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, toMemberResponse(view))
	return nil
}

func (h *Handler) changeMemberRole(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	targetID, err := pathID(r, "userId")
	if err != nil {
		return err
	}
	var req changeRoleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}

	view, err := h.deps.ChangeMemberRole.Execute(r.Context(), trip.ChangeMemberRoleInput{
		TripID:   trip.ID(id),
		ActorID:  authapi.UserID(r.Context()),
		TargetID: user.ID(targetID),
		Role:     req.Role,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toMemberResponse(view))
	return nil
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	targetID, err := pathID(r, "userId")
	if err != nil {
		return err
	}

	err = h.deps.RemoveMember.Execute(r.Context(), trip.RemoveMemberInput{
		TripID:   trip.ID(id),
		ActorID:  authapi.UserID(r.Context()),
		TargetID: user.ID(targetID),
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func pathID(r *http.Request, name string) (string, error) {
	id := r.PathValue(name)
	if !ids.IsValid(id) {
		return "", apperror.BadRequest("invalid_id", fmt.Sprintf("The %s in the path must be a lowercase UUID.", name))
	}
	return id, nil
}

func toTripResponse(v trip.View) tripResponse {
	t := v.Trip
	return tripResponse{
		ID:          string(t.ID),
		Name:        t.Name,
		Destination: t.Destination,
		StartDate:   string(t.StartDate),
		EndDate:     string(t.EndDate),
		Timezone:    string(t.Timezone),
		Currency:    string(t.Currency),
		OwnerID:     string(t.OwnerID()),
		MyRole:      string(v.Role),
		Version:     t.Version,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
}

func toMemberResponse(v trip.MemberView) memberResponse {
	return memberResponse{
		UserID:    string(v.Member.UserID),
		Name:      v.User.Name,
		Email:     v.User.Email,
		Role:      string(v.Member.Role),
		CreatedAt: v.Member.CreatedAt,
		UpdatedAt: v.Member.UpdatedAt,
	}
}
