// Package apitest builds a complete in-memory API (auth, trips and a real trip.Authorizer) so a
// feature's HTTP tests only add their own module and talk to it like a client would.
package apitest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rinotravel-api/internal/auth"
	"rinotravel-api/internal/auth/authtest"
	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/platform/httpx"
	"rinotravel-api/internal/platform/logging"
	"rinotravel-api/internal/server"
	"rinotravel-api/internal/trip"
	tripapi "rinotravel-api/internal/trip/httpapi"
	"rinotravel-api/internal/trip/triptest"
	"rinotravel-api/internal/user"
)

const RegistrationCode = "dev-registration-code"

type Account struct {
	Name  string
	Email string
	ID    string
	Token string
}

// Env exposes what feature modules need to be built, plus helpers to drive the API.
type Env struct {
	Handler http.Handler
	Logger  *slog.Logger
	Logs    *bytes.Buffer
	Guard   authapi.Guard
	Authz   *trip.Authorizer
	Trips   *triptest.Repository
	Users   *authtest.Users
}

// New builds the API. build receives the shared pieces and returns the feature modules to mount.
func New(t *testing.T, build func(Env) []server.Module) *Env {
	t.Helper()

	users, sessions := authtest.NewUsers(), authtest.NewSessions()
	hasher := &authtest.Hasher{}
	login, err := auth.NewLogin(users, sessions, hasher)
	if err != nil {
		t.Fatal(err)
	}
	trips := triptest.NewRepository()

	var logs bytes.Buffer
	logger := logging.New(&logs, slog.LevelDebug, true)
	guard := authapi.NewGuard(auth.NewAuthenticate(sessions))

	env := &Env{Logger: logger, Logs: &logs, Guard: guard, Authz: trip.NewAuthorizer(trips), Trips: trips, Users: users}

	modules := []server.Module{
		authapi.New(authapi.Deps{
			Logger:      logger,
			Guard:       guard,
			RateLimiter: httpx.NewRateLimiter(100000, time.Minute, httpx.ClientIP(false)),
			Register:    auth.NewRegister(users, sessions, hasher, RegistrationCode),
			Login:       login,
			Logout:      auth.NewLogout(sessions),
			GetUser:     user.NewGetUser(users),
		}),
		tripapi.New(tripapi.Deps{
			Logger:            logger,
			Guard:             guard,
			CreateTrip:        trip.NewCreateTrip(trips),
			GetTrip:           trip.NewGetTrip(trips),
			ListTrips:         trip.NewListTrips(trips),
			UpdateTrip:        trip.NewUpdateTrip(trips),
			DeleteTrip:        trip.NewDeleteTrip(trips),
			AddMember:         trip.NewAddMember(trips, users),
			ListMembers:       trip.NewListMembers(trips, users),
			ChangeMemberRole:  trip.NewChangeMemberRole(trips, users),
			RemoveMember:      trip.NewRemoveMember(trips),
			TransferOwnership: trip.NewTransferOwnership(trips),
		}),
	}
	modules = append(modules, build(*env)...)
	env.Handler = server.NewHandler(server.Options{Logger: logger, Modules: modules})
	return env
}

// TripHandler builds the trip HTTP handler over the environment's repositories, for tests that need
// its sync source.
func TripHandler(e Env) *tripapi.Handler {
	return tripapi.New(tripapi.Deps{
		Logger: e.Logger, Guard: e.Guard,
		CreateTrip: trip.NewCreateTrip(e.Trips), GetTrip: trip.NewGetTrip(e.Trips), ListTrips: trip.NewListTrips(e.Trips),
		UpdateTrip: trip.NewUpdateTrip(e.Trips), DeleteTrip: trip.NewDeleteTrip(e.Trips),
		AddMember: trip.NewAddMember(e.Trips, e.Users), ListMembers: trip.NewListMembers(e.Trips, e.Users),
		ChangeMemberRole: trip.NewChangeMemberRole(e.Trips, e.Users), RemoveMember: trip.NewRemoveMember(e.Trips),
		TransferOwnership: trip.NewTransferOwnership(e.Trips),
	})
}

func (e *Env) Do(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.Handler.ServeHTTP(rec, req)
	return rec
}

func Decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body, err)
	}
	return body
}

func (e *Env) Signup(t *testing.T, name string) Account {
	t.Helper()
	email := strings.ToLower(name) + "@example.com"
	body := fmt.Sprintf(`{"email":%q,"name":%q,"password":"correct horse battery","registrationCode":%q}`, email, name, RegistrationCode)
	rec := e.Do(http.MethodPost, "/api/v1/auth/register", "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup status = %d, body %s", rec.Code, rec.Body)
	}
	res := Decode(t, rec)
	return Account{Name: name, Email: email, ID: res["user"].(map[string]any)["id"].(string), Token: res["token"].(string)}
}

const TripBody = `{"name":"Japan 2027","destination":"Tokyo","startDate":"2027-04-01","endDate":"2027-04-15","timezone":"Asia/Tokyo","currency":"JPY"}`

// CreateTrip creates a trip owned by owner and returns its id.
func (e *Env) CreateTrip(t *testing.T, owner Account) string {
	t.Helper()
	rec := e.Do(http.MethodPost, "/api/v1/trips", owner.Token, TripBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create trip status = %d, body %s", rec.Code, rec.Body)
	}
	return Decode(t, rec)["id"].(string)
}

func (e *Env) AddMember(t *testing.T, tripID string, actor, member Account, role string) {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"role":%q}`, member.Email, role)
	if rec := e.Do(http.MethodPost, "/api/v1/trips/"+tripID+"/members", actor.Token, body); rec.Code != http.StatusCreated {
		t.Fatalf("add member status = %d, body %s", rec.Code, rec.Body)
	}
}

// Create posts body to path and returns the created resource, failing unless it is a 201.
func (e *Env) Create(t *testing.T, path, token, body string) map[string]any {
	t.Helper()
	rec := e.Do(http.MethodPost, path, token, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST %s status = %d, body %s", path, rec.Code, rec.Body)
	}
	return Decode(t, rec)
}

func RequireProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	body := Decode(t, rec)
	if rec.Code != status || body["code"] != code {
		t.Fatalf("status = %d, body %s; want %d %s", rec.Code, rec.Body, status, code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	return body
}

// Fields returns the field names listed in a 422 problem.
func Fields(body map[string]any) map[string]bool {
	out := map[string]bool{}
	if list, ok := body["errors"].([]any); ok {
		for _, item := range list {
			out[item.(map[string]any)["field"].(string)] = true
		}
	}
	return out
}
