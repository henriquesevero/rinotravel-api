package httpapi_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rinotravel-api/internal/auth"
	"rinotravel-api/internal/auth/authtest"
	"rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/platform/logging"
	"rinotravel-api/internal/server"
	"rinotravel-api/internal/user"
)

const registrationCode = "dev-registration-code"

type env struct {
	handler  http.Handler
	users    *authtest.Users
	sessions *authtest.Sessions
	logs     *bytes.Buffer
}

func newEnv(t *testing.T, rateLimit int) env {
	t.Helper()

	users, sessions := authtest.NewUsers(), authtest.NewSessions()
	hasher := &authtest.Hasher{}
	login, err := auth.NewLogin(users, sessions, hasher)
	if err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	logger := logging.New(&logs, slog.LevelDebug, true)
	api := httpapi.New(httpapi.Deps{
		Logger:      logger,
		Guard:       httpapi.NewGuard(auth.NewAuthenticate(sessions)),
		RateLimiter: httpx.NewRateLimiter(rateLimit, time.Minute, httpx.ClientIP(false)),
		Register:    auth.NewRegister(users, sessions, hasher, registrationCode),
		Login:       login,
		Logout:      auth.NewLogout(sessions),
		GetUser:     user.NewGetUser(users),
	})

	return env{
		handler:  server.NewHandler(server.Options{Logger: logger, Modules: []server.Module{api}}),
		users:    users,
		sessions: sessions,
		logs:     &logs,
	}
}

func (e env) do(method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body, err)
	}
	return body
}

const registerBody = `{"email":"Ana@Example.com","name":"Ana","password":"correct horse battery","registrationCode":"` + registrationCode + `"}`

func registerAna(t *testing.T, e env) (token string) {
	t.Helper()
	rec := e.do(http.MethodPost, "/api/v1/auth/register", registerBody, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body %s", rec.Code, rec.Body)
	}
	return decode(t, rec)["token"].(string)
}

func TestRegister_ReturnsSessionAndUser(t *testing.T) {
	e := newEnv(t, 100)

	rec := e.do(http.MethodPost, "/api/v1/auth/register", registerBody, "")

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	body := decode(t, rec)
	userBody, _ := body["user"].(map[string]any)
	if !strings.HasPrefix(body["token"].(string), "rt_") || body["expiresAt"] == nil ||
		userBody["email"] != "ana@example.com" || userBody["name"] != "Ana" || userBody["id"] == "" {
		t.Errorf("unexpected body: %v", body)
	}
	if _, leaked := userBody["passwordHash"]; leaked || strings.Contains(rec.Body.String(), "hashed:") {
		t.Errorf("response leaks the password hash: %s", rec.Body)
	}
}

func TestRegister_ErrorResponses(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"wrong code", strings.Replace(registerBody, registrationCode, "nope", 1), http.StatusForbidden, "invalid_registration_code"},
		{"invalid fields", strings.Replace(registerBody, "correct horse battery", "short", 1), http.StatusUnprocessableEntity, "validation_failed"},
		{"malformed json", `{"email":`, http.StatusBadRequest, "malformed_json"},
		{"unknown field", `{"email":"a@b.co","role":"admin"}`, http.StatusBadRequest, "malformed_json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, 100)

			rec := e.do(http.MethodPost, "/api/v1/auth/register", tt.body, "")

			if rec.Code != tt.wantStatus || decode(t, rec)["code"] != tt.wantCode {
				t.Errorf("status = %d, body %s; want %d %s", rec.Code, rec.Body, tt.wantStatus, tt.wantCode)
			}
			if e.users.Len() != 0 {
				t.Error("a failed registration created a user")
			}
		})
	}
}

func TestRegister_DuplicateEmailIsConflict(t *testing.T) {
	e := newEnv(t, 100)
	registerAna(t, e)

	rec := e.do(http.MethodPost, "/api/v1/auth/register", registerBody, "")

	if rec.Code != http.StatusConflict || decode(t, rec)["code"] != "email_taken" {
		t.Errorf("status = %d, body %s", rec.Code, rec.Body)
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t, 100)
	registerAna(t, e)

	t.Run("valid credentials", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/api/v1/auth/login", `{"email":"ana@example.com","password":"correct horse battery"}`, "")

		if rec.Code != http.StatusOK || !strings.HasPrefix(decode(t, rec)["token"].(string), "rt_") {
			t.Errorf("status = %d, body %s", rec.Code, rec.Body)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/api/v1/auth/login", `{"email":"ana@example.com","password":"wrong wrong wrong"}`, "")

		if rec.Code != http.StatusUnauthorized || decode(t, rec)["code"] != "invalid_credentials" {
			t.Errorf("status = %d, body %s", rec.Code, rec.Body)
		}
	})
}

func TestMe(t *testing.T) {
	e := newEnv(t, 100)
	token := registerAna(t, e)

	t.Run("returns the authenticated user", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/api/v1/me", "", token)

		body := decode(t, rec)
		if rec.Code != http.StatusOK || body["email"] != "ana@example.com" {
			t.Errorf("status = %d, body %s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "password") {
			t.Errorf("response mentions the password: %s", rec.Body)
		}
	})

	t.Run("missing credentials", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/api/v1/me", "", "")

		if rec.Code != http.StatusUnauthorized || decode(t, rec)["code"] != "unauthenticated" {
			t.Errorf("status = %d, body %s", rec.Code, rec.Body)
		}
		if rec.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("WWW-Authenticate = %q, want Bearer", rec.Header().Get("WWW-Authenticate"))
		}
	})

	t.Run("unknown token", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/api/v1/me", "", "rt_unknown")

		if rec.Code != http.StatusUnauthorized || decode(t, rec)["code"] != "invalid_token" {
			t.Errorf("status = %d, body %s", rec.Code, rec.Body)
		}
	})

	t.Run("expired session", func(t *testing.T) {
		expired := newEnv(t, 100)
		expiredToken := registerAna(t, expired)
		expired.sessions.ExpireAll()

		rec := expired.do(http.MethodGet, "/api/v1/me", "", expiredToken)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})

	for name, header := range map[string]string{
		"basic scheme":  "Basic dXNlcjpwYXNz",
		"bearer only":   "Bearer",
		"bearer blank":  "Bearer   ",
		"token no type": token,
	} {
		t.Run("malformed authorization: "+name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
			req.Header.Set("Authorization", header)
			rec := httptest.NewRecorder()
			e.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
		})
	}

	t.Run("scheme is case insensitive", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		req.Header.Set("Authorization", "bearer "+token)
		rec := httptest.NewRecorder()
		e.handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})
}

func TestLogout_RevokesTheToken(t *testing.T) {
	e := newEnv(t, 100)
	token := registerAna(t, e)

	rec := e.do(http.MethodPost, "/api/v1/auth/logout", "", token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", rec.Code)
	}

	if rec := e.do(http.MethodGet, "/api/v1/me", "", token); rec.Code != http.StatusUnauthorized {
		t.Errorf("/me after logout = %d, want 401", rec.Code)
	}
	if rec := e.do(http.MethodPost, "/api/v1/auth/logout", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("logout without token = %d, want 401", rec.Code)
	}
}

func TestAuthEndpointsAreRateLimited(t *testing.T) {
	e := newEnv(t, 3)
	body := `{"email":"ana@example.com","password":"wrong wrong wrong"}`

	for i := 0; i < 3; i++ {
		if rec := e.do(http.MethodPost, "/api/v1/auth/login", body, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i+1, rec.Code)
		}
	}

	rec := e.do(http.MethodPost, "/api/v1/auth/login", body, "")
	if rec.Code != http.StatusTooManyRequests || decode(t, rec)["code"] != "rate_limited" {
		t.Errorf("status = %d, body %s; want 429 rate_limited", rec.Code, rec.Body)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response has no Retry-After")
	}
	if rec := e.do(http.MethodPost, "/api/v1/auth/register", registerBody, ""); rec.Code != http.StatusTooManyRequests {
		t.Errorf("register status = %d, want 429 (limit is shared per client)", rec.Code)
	}
}

func TestSecretsNeverReachTheLogs(t *testing.T) {
	e := newEnv(t, 100)
	token := registerAna(t, e)
	e.do(http.MethodGet, "/api/v1/me", "", token)
	e.do(http.MethodPost, "/api/v1/auth/login", `{"email":"ana@example.com","password":"correct horse battery"}`, "")

	logs := e.logs.String()
	for _, secret := range []string{"correct horse battery", registrationCode, token} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain a secret (%q...)", secret[:6])
		}
	}
}

func TestUserIDIsAbsentOutsideGuard(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if id := httpapi.UserID(req.Context()); id != "" {
		t.Errorf("UserID() = %q, want empty", id)
	}
}
