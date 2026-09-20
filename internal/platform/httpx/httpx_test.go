package httpx_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/platform/logging"
)

func newLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return logging.New(&buf, slog.LevelDebug, true), &buf
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	return body
}

func TestHandle_MapsAppErrorsToProblems(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"bad request", apperror.BadRequest("malformed_json", "bad"), http.StatusBadRequest, "malformed_json"},
		{"unauthorized", apperror.Unauthorized("invalid_credentials", "bad"), http.StatusUnauthorized, "invalid_credentials"},
		{"forbidden", apperror.Forbidden("forbidden", "bad"), http.StatusForbidden, "forbidden"},
		{"not found", apperror.NotFound("trip_not_found", "bad"), http.StatusNotFound, "trip_not_found"},
		{"conflict", apperror.Conflict("member_exists", "bad"), http.StatusConflict, "member_exists"},
		{"validation", apperror.Validation(), http.StatusUnprocessableEntity, "validation_failed"},
		{"too many requests", apperror.TooManyRequests("rate_limited", "bad"), http.StatusTooManyRequests, "rate_limited"},
		{"wrapped app error", errors.Join(errors.New("ctx"), apperror.NotFound("x", "y")), http.StatusNotFound, "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := newLogger()
			h := httpx.Handle(logger, func(http.ResponseWriter, *http.Request) error { return tt.err })

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			body := decodeProblem(t, rec)
			if body["code"] != tt.wantCode {
				t.Errorf("code = %v, want %s", body["code"], tt.wantCode)
			}
			if body["title"] != http.StatusText(tt.wantStatus) {
				t.Errorf("title = %v, want %s", body["title"], http.StatusText(tt.wantStatus))
			}
		})
	}
}

func TestHandle_IncludesFieldErrors(t *testing.T) {
	logger, _ := newLogger()
	h := httpx.Handle(logger, func(http.ResponseWriter, *http.Request) error {
		return apperror.Validation(apperror.FieldError{Field: "name", Message: "is required"})
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := decodeProblem(t, rec)
	fields, _ := body["errors"].([]any)
	if len(fields) != 1 {
		t.Fatalf("errors = %v, want one entry", body["errors"])
	}
	first, _ := fields[0].(map[string]any)
	if first["field"] != "name" || first["message"] != "is required" {
		t.Errorf("unexpected field error: %v", first)
	}
}

func TestHandle_HidesInternalErrorDetails(t *testing.T) {
	logger, logs := newLogger()
	h := httpx.Chain(
		httpx.Handle(logger, func(http.ResponseWriter, *http.Request) error {
			return errors.New("dial tcp 10.0.0.5:27017: secret-host refused")
		}),
		httpx.RequestID,
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(httpx.HeaderRequestID, "req-42")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret-host") {
		t.Errorf("response leaked internal error: %s", rec.Body)
	}
	body := decodeProblem(t, rec)
	if body["code"] != "internal_error" || body["requestId"] != "req-42" {
		t.Errorf("unexpected problem: %v", body)
	}
	if !strings.Contains(logs.String(), "secret-host") || !strings.Contains(logs.String(), "req-42") {
		t.Errorf("internal error was not logged with request id: %s", logs)
	}
}

func TestHandle_LeavesSuccessfulResponsesAlone(t *testing.T) {
	logger, _ := newLogger()
	h := httpx.Handle(logger, func(w http.ResponseWriter, _ *http.Request) error {
		httpx.WriteJSON(w, http.StatusCreated, map[string]string{"id": "1"})
		return nil
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusCreated || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("status = %d, content-type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if strings.TrimSpace(rec.Body.String()) != `{"id":"1"}` {
		t.Errorf("body = %q", rec.Body)
	}
}

func TestRequestID(t *testing.T) {
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger, logs := newLogger()
		logger.InfoContext(r.Context(), "probe")
		seen = logs.String()
	})
	h := httpx.RequestID(next)

	t.Run("keeps a valid incoming id", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(httpx.HeaderRequestID, "client-id-1")
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get(httpx.HeaderRequestID); got != "client-id-1" {
			t.Errorf("response id = %q, want client-id-1", got)
		}
		if !strings.Contains(seen, "client-id-1") {
			t.Errorf("id was not propagated through context: %s", seen)
		}
	})

	t.Run("replaces an invalid incoming id", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(httpx.HeaderRequestID, "bad id\twith spaces")
		h.ServeHTTP(rec, req)

		got := rec.Header().Get(httpx.HeaderRequestID)
		if got == "" || strings.ContainsAny(got, " \t") {
			t.Errorf("response id = %q, want a generated id", got)
		}
	})

	t.Run("generates an id when absent", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if rec.Header().Get(httpx.HeaderRequestID) == "" {
			t.Error("response has no request id")
		}
	})
}

func TestRecover(t *testing.T) {
	logger, logs := newLogger()
	h := httpx.Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("response leaked panic value: %s", rec.Body)
	}
	if body := decodeProblem(t, rec); body["code"] != "internal_error" {
		t.Errorf("unexpected problem: %v", body)
	}
	if !strings.Contains(logs.String(), "panic recovered") || !strings.Contains(logs.String(), "boom") {
		t.Errorf("panic was not logged: %s", logs)
	}
}

func TestRecover_RepanicsOnAbortHandler(t *testing.T) {
	logger, _ := newLogger()
	h := httpx.Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		got, _ := recover().(error)
		if !errors.Is(got, http.ErrAbortHandler) {
			t.Errorf("recover() = %v, want http.ErrAbortHandler", got)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestAccessLog(t *testing.T) {
	logger, logs := newLogger()
	h := httpx.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }),
		httpx.RequestID,
		httpx.AccessLog(logger),
	)

	req := httptest.NewRequest(http.MethodPost, "/things?secret=1", nil)
	req.Header.Set(httpx.HeaderRequestID, "req-7")
	h.ServeHTTP(httptest.NewRecorder(), req)

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("access log is not JSON: %v", err)
	}
	if entry["method"] != "POST" || entry["path"] != "/things" || entry["status"] != float64(http.StatusTeapot) || entry["request_id"] != "req-7" {
		t.Errorf("unexpected access log: %v", entry)
	}
	if strings.Contains(logs.String(), "secret") {
		t.Errorf("access log leaked the query string: %s", logs)
	}
}

func TestAccessLog_DefaultsToStatusOK(t *testing.T) {
	logger, logs := newLogger()
	h := httpx.AccessLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("access log is not JSON: %v", err)
	}
	if entry["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want 200", entry["status"])
	}
}

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := httpx.CORS([]string{"https://app.example"})(next)

	t.Run("allowed origin", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://app.example")
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
			t.Errorf("Allow-Origin = %q", got)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("disallowed origin", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://evil.example")
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Allow-Origin = %q, want empty", got)
		}
		if rec.Header().Get("Vary") != "Origin" {
			t.Errorf("Vary = %q, want Origin", rec.Header().Get("Vary"))
		}
	})

	t.Run("preflight", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/trips", nil)
		req.Header.Set("Origin", "https://app.example")
		req.Header.Set("Access-Control-Request-Method", "PATCH")
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, want 204", rec.Code)
		}
		if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "PATCH") {
			t.Errorf("Allow-Methods = %q", rec.Header().Get("Access-Control-Allow-Methods"))
		}
		if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
			t.Errorf("Allow-Headers = %q", rec.Header().Get("Access-Control-Allow-Headers"))
		}
	})
}

func TestChain_FirstMiddlewareIsOutermost(t *testing.T) {
	var order []string
	tag := func(name string) httpx.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := httpx.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	}), tag("first"), tag("second"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if got := strings.Join(order, ","); got != "first,second,handler" {
		t.Errorf("order = %s", got)
	}
}
