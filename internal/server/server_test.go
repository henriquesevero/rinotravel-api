package server_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"rinotravel-api/internal/platform/logging"
	"rinotravel-api/internal/server"
)

func newHandler(t *testing.T) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	return server.NewHandler(server.Options{
		Logger:             logging.New(&logs, slog.LevelInfo, true),
		CORSAllowedOrigins: []string{"https://app.example"},
	}), &logs
}

func TestHealth(t *testing.T) {
	h, _ := newHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if len(body) != 1 || body["status"] != "ok" {
		t.Errorf("body = %v, want {status: ok}", body)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("response has no X-Request-ID")
	}
}

func TestUnknownRouteReturnsProblem(t *testing.T) {
	h, _ := newHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["code"] != "route_not_found" || body["requestId"] == "" {
		t.Errorf("unexpected problem: %v", body)
	}
}

func TestRequestsAreLoggedWithRoute(t *testing.T) {
	h, logs := newHandler(t)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("access log is not JSON: %v", err)
	}
	if entry["route"] != "GET /api/v1/health" || entry["status"] != float64(http.StatusOK) || entry["request_id"] == nil {
		t.Errorf("unexpected access log: %v", entry)
	}
}

func TestCORSIsApplied(t *testing.T) {
	h, _ := newHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/trips", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
}
