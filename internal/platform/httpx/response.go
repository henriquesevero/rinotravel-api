package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/requestid"
)

type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

func Handle(logger *slog.Logger, fn HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			writeError(w, r, logger, err)
		}
	})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, "application/json", v)
}

type problem struct {
	Status    int          `json:"status"`
	Title     string       `json:"title"`
	Code      string       `json:"code"`
	Detail    string       `json:"detail"`
	RequestID string       `json:"requestId,omitempty"`
	Errors    []fieldError `json:"errors,omitempty"`
}

type fieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		logger.ErrorContext(r.Context(), "unhandled error",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Any("error", err),
		)
		writeInternalError(w, r)
		return
	}

	p := problem{
		Status: statusFor(appErr.Kind),
		Code:   appErr.Code,
		Detail: appErr.Message,
	}
	for _, f := range appErr.Fields {
		p.Errors = append(p.Errors, fieldError{Field: f.Field, Message: f.Message})
	}
	writeProblem(w, r, p)
}

func writeInternalError(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, r, problem{
		Status: http.StatusInternalServerError,
		Code:   "internal_error",
		Detail: "An unexpected error occurred.",
	})
}

func writeProblem(w http.ResponseWriter, r *http.Request, p problem) {
	p.Title = http.StatusText(p.Status)
	p.RequestID = requestid.FromContext(r.Context())
	writeJSON(w, p.Status, "application/problem+json", p)
}

func statusFor(kind apperror.Kind) int {
	switch kind {
	case apperror.KindBadRequest:
		return http.StatusBadRequest
	case apperror.KindUnauthorized:
		return http.StatusUnauthorized
	case apperror.KindForbidden:
		return http.StatusForbidden
	case apperror.KindNotFound:
		return http.StatusNotFound
	case apperror.KindConflict:
		return http.StatusConflict
	case apperror.KindValidation:
		return http.StatusUnprocessableEntity
	case apperror.KindTooManyRequests:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, status int, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
