package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"rinotravel-api/internal/auth"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/user"
)

type Deps struct {
	Logger      *slog.Logger
	Guard       Guard
	RateLimiter *httpx.RateLimiter
	Register    *auth.Register
	Login       *auth.Login
	Logout      *auth.Logout
	GetUser     *user.GetUser
}

type Handler struct {
	deps Deps
}

func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	d := h.deps
	limited := d.RateLimiter.Wrap

	mux.Handle("POST /api/v1/auth/register", httpx.Handle(d.Logger, limited(h.register)))
	mux.Handle("POST /api/v1/auth/login", httpx.Handle(d.Logger, limited(h.login)))
	mux.Handle("POST /api/v1/auth/logout", httpx.Handle(d.Logger, d.Guard.Require(h.logout)))
	mux.Handle("GET /api/v1/me", httpx.Handle(d.Logger, d.Guard.Require(h.me)))
}

type registerRequest struct {
	Email            string `json:"email"`
	Name             string `json:"name"`
	Password         string `json:"password"`
	RegistrationCode string `json:"registrationCode"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Token     string       `json:"token"`
	ExpiresAt time.Time    `json:"expiresAt"`
	User      userResponse `json:"user"`
}

type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) error {
	var req registerRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}

	result, err := h.deps.Register.Execute(r.Context(), auth.RegisterInput{
		Email:            req.Email,
		Name:             req.Name,
		Password:         req.Password,
		RegistrationCode: req.RegistrationCode,
	})
	if err != nil {
		return err
	}
	writeSession(w, http.StatusCreated, result)
	return nil
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}

	result, err := h.deps.Login.Execute(r.Context(), auth.LoginInput{Email: req.Email, Password: req.Password})
	if err != nil {
		return err
	}
	writeSession(w, http.StatusOK, result)
	return nil
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) error {
	token, _ := bearerToken(r)
	if err := h.deps.Logout.Execute(r.Context(), token); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) error {
	u, err := h.deps.GetUser.Execute(r.Context(), UserID(r.Context()))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toUserResponse(u))
	return nil
}

func writeSession(w http.ResponseWriter, status int, result auth.Result) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, status, sessionResponse{
		Token:     result.Token,
		ExpiresAt: result.ExpiresAt,
		User:      toUserResponse(result.User),
	})
}

func toUserResponse(u user.User) userResponse {
	return userResponse{ID: string(u.ID), Email: u.Email, Name: u.Name, CreatedAt: u.CreatedAt}
}
