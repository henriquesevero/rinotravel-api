package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestLimiter(max int, clock *time.Time) *RateLimiter {
	l := NewRateLimiter(max, time.Minute, func(r *http.Request) string { return r.Header.Get("X-Test-Key") })
	l.now = func() time.Time { return *clock }
	return l
}

func call(l *RateLimiter, key string) (*httptest.ResponseRecorder, error) {
	handler := l.Wrap(func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusNoContent)
		return nil
	})
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Test-Key", key)
	rec := httptest.NewRecorder()
	return rec, handler(rec, req)
}

func TestRateLimiter_BlocksAfterLimitWithinWindow(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	l := newTestLimiter(2, &now)

	for i := 0; i < 2; i++ {
		if _, err := call(l, "a"); err != nil {
			t.Fatalf("request %d error = %v, want allowed", i+1, err)
		}
	}

	now = now.Add(20 * time.Second)
	rec, err := call(l, "a")
	if err == nil {
		t.Fatal("third request was allowed, want rate limited")
	}
	if got := rec.Header().Get("Retry-After"); got != "40" {
		t.Errorf("Retry-After = %q, want 40", got)
	}
}

func TestRateLimiter_KeysAreIndependent(t *testing.T) {
	now := time.Now()
	l := newTestLimiter(1, &now)

	if _, err := call(l, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := call(l, "b"); err != nil {
		t.Errorf("a different key was limited: %v", err)
	}
	if _, err := call(l, "a"); err == nil {
		t.Error("the same key was not limited")
	}
}

func TestRateLimiter_AllowsAgainAfterWindow(t *testing.T) {
	now := time.Now()
	l := newTestLimiter(1, &now)

	if _, err := call(l, "a"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)

	if _, err := call(l, "a"); err != nil {
		t.Errorf("request after the window was limited: %v", err)
	}
}

func TestRateLimiter_SweepsExpiredEntries(t *testing.T) {
	now := time.Now()
	l := newTestLimiter(1, &now)

	for _, key := range []string{"a", "b", "c"} {
		if _, err := call(l, key); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Minute)
	if _, err := call(l, "d"); err != nil {
		t.Fatal(err)
	}

	if len(l.entries) != 1 {
		t.Errorf("entries = %d, want only the active one", len(l.entries))
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		trustProxy bool
		remoteAddr string
		forwarded  string
		want       string
	}{
		{"direct connection", false, "203.0.113.7:51000", "", "203.0.113.7"},
		{"ignores forwarded header when proxy is not trusted", false, "10.0.0.1:1234", "198.51.100.9", "10.0.0.1"},
		{"uses the address the proxy observed", true, "10.0.0.1:1234", "198.51.100.9", "198.51.100.9"},
		{"ignores client-supplied earlier entries", true, "10.0.0.1:1234", "1.2.3.4, 198.51.100.9", "198.51.100.9"},
		{"falls back without the header", true, "10.0.0.1:1234", "", "10.0.0.1"},
		{"address without port", false, "unix-socket", "", "unix-socket"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tt.forwarded)
			}

			if got := ClientIP(tt.trustProxy)(req); got != tt.want {
				t.Errorf("ClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
