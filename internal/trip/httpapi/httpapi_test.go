package httpapi_test

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
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/platform/logging"
	"rinotravel-api/internal/server"
	"rinotravel-api/internal/trip"
	tripapi "rinotravel-api/internal/trip/httpapi"
	"rinotravel-api/internal/trip/triptest"
	"rinotravel-api/internal/user"
)

const registrationCode = "dev-registration-code"

type env struct {
	handler http.Handler
	logs    *bytes.Buffer
}

type account struct {
	token string
	id    string
	email string
}

func newEnv(t *testing.T) env {
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

	authAPI := authapi.New(authapi.Deps{
		Logger:      logger,
		Guard:       guard,
		RateLimiter: httpx.NewRateLimiter(10000, time.Minute, httpx.ClientIP(false)),
		Register:    auth.NewRegister(users, sessions, hasher, registrationCode),
		Login:       login,
		Logout:      auth.NewLogout(sessions),
		GetUser:     user.NewGetUser(users),
	})
	tripAPI := tripapi.New(tripapi.Deps{
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
	})

	return env{
		handler: server.NewHandler(server.Options{Logger: logger, Modules: []server.Module{authAPI, tripAPI}}),
		logs:    &logs,
	}
}

func (e env) do(method, path, token, body string) *httptest.ResponseRecorder {
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

func (e env) signup(t *testing.T, name string) account {
	t.Helper()
	email := name + "@example.com"
	body := fmt.Sprintf(`{"email":%q,"name":%q,"password":"correct horse battery","registrationCode":%q}`, email, name, registrationCode)
	rec := e.do(http.MethodPost, "/api/v1/auth/register", "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signup status = %d, body %s", rec.Code, rec.Body)
	}
	res := decode(t, rec)
	return account{token: res["token"].(string), id: res["user"].(map[string]any)["id"].(string), email: email}
}

const tripBody = `{"name":"Japan 2027","destination":"Tokyo","startDate":"2027-04-01","endDate":"2027-04-15","timezone":"Asia/Tokyo","currency":"JPY"}`

func (e env) createTrip(t *testing.T, owner account) map[string]any {
	t.Helper()
	rec := e.do(http.MethodPost, "/api/v1/trips", owner.token, tripBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create trip status = %d, body %s", rec.Code, rec.Body)
	}
	return decode(t, rec)
}

func (e env) addMember(t *testing.T, tripID string, actor, member account, role string) {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"role":%q}`, member.email, role)
	if rec := e.do(http.MethodPost, "/api/v1/trips/"+tripID+"/members", actor.token, body); rec.Code != http.StatusCreated {
		t.Fatalf("add member status = %d, body %s", rec.Code, rec.Body)
	}
}

func requireProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	body := decode(t, rec)
	if rec.Code != status || body["code"] != code {
		t.Fatalf("status = %d, body %s; want %d %s", rec.Code, rec.Body, status, code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	return body
}

func TestAllRoutesRequireAuthentication(t *testing.T) {
	e := newEnv(t)
	id := ids.New()

	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/trips"},
		{"POST", "/api/v1/trips"},
		{"GET", "/api/v1/trips/" + id},
		{"PATCH", "/api/v1/trips/" + id},
		{"DELETE", "/api/v1/trips/" + id},
		{"POST", "/api/v1/trips/" + id + "/transfer-ownership"},
		{"GET", "/api/v1/trips/" + id + "/members"},
		{"POST", "/api/v1/trips/" + id + "/members"},
		{"PATCH", "/api/v1/trips/" + id + "/members/" + id},
		{"DELETE", "/api/v1/trips/" + id + "/members/" + id},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			requireProblem(t, e.do(route.method, route.path, "", "{}"), http.StatusUnauthorized, "unauthenticated")
		})
	}
}

func TestCreateTrip(t *testing.T) {
	e := newEnv(t)
	ana := e.signup(t, "ana")

	t.Run("creates the trip and returns it with the caller as owner", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/api/v1/trips", ana.token, tripBody)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		body := decode(t, rec)
		if body["name"] != "Japan 2027" || body["timezone"] != "Asia/Tokyo" || body["currency"] != "JPY" ||
			body["startDate"] != "2027-04-01" || body["ownerId"] != ana.id || body["myRole"] != "OWNER" || body["version"] != float64(1) {
			t.Errorf("unexpected body: %v", body)
		}
		if got := rec.Header().Get("Location"); got != "/api/v1/trips/"+body["id"].(string) {
			t.Errorf("Location = %q", got)
		}
	})

	t.Run("accepts a client generated id and rejects reuse", func(t *testing.T) {
		id := ids.New()
		body := strings.Replace(tripBody, "{", `{"id":"`+id+`",`, 1)

		rec := e.do(http.MethodPost, "/api/v1/trips", ana.token, body)
		if rec.Code != http.StatusCreated || decode(t, rec)["id"] != id {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		bia := e.signup(t, "bia")
		requireProblem(t, e.do(http.MethodPost, "/api/v1/trips", bia.token, body), http.StatusConflict, "trip_id_taken")
	})

	t.Run("validation errors list every field", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/api/v1/trips", ana.token, `{"name":"","startDate":"2027-04-15","endDate":"2027-04-01","timezone":"Mars/Base","currency":"XXX"}`)

		body := requireProblem(t, rec, http.StatusUnprocessableEntity, "validation_failed")
		fields := map[string]bool{}
		for _, f := range body["errors"].([]any) {
			fields[f.(map[string]any)["field"].(string)] = true
		}
		for _, want := range []string{"name", "destination", "endDate", "timezone", "currency"} {
			if !fields[want] {
				t.Errorf("missing error for %s in %v", want, body["errors"])
			}
		}
	})

	t.Run("malformed and unexpected bodies", func(t *testing.T) {
		requireProblem(t, e.do(http.MethodPost, "/api/v1/trips", ana.token, `{"name":`), http.StatusBadRequest, "malformed_json")
		requireProblem(t, e.do(http.MethodPost, "/api/v1/trips", ana.token, `{"name":"x","ownerId":"someone"}`), http.StatusBadRequest, "malformed_json")
	})

	t.Run("a malformed client id is a validation error", func(t *testing.T) {
		body := strings.Replace(tripBody, "{", `{"id":"nope",`, 1)
		requireProblem(t, e.do(http.MethodPost, "/api/v1/trips", ana.token, body), http.StatusUnprocessableEntity, "validation_failed")
	})
}

func TestGetAndListTrips(t *testing.T) {
	e := newEnv(t)
	ana, bia, caio := e.signup(t, "ana"), e.signup(t, "bia"), e.signup(t, "caio")
	created := e.createTrip(t, ana)
	id := created["id"].(string)
	e.addMember(t, id, ana, bia, "VIEWER")

	t.Run("a member reads with their own role", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/api/v1/trips/"+id, bia.token, "")

		body := decode(t, rec)
		if rec.Code != http.StatusOK || body["myRole"] != "VIEWER" || body["ownerId"] != ana.id {
			t.Errorf("status = %d, body %v", rec.Code, body)
		}
	})

	t.Run("an outsider cannot tell the trip exists", func(t *testing.T) {
		existing := requireProblem(t, e.do(http.MethodGet, "/api/v1/trips/"+id, caio.token, ""), http.StatusNotFound, "trip_not_found")
		missing := requireProblem(t, e.do(http.MethodGet, "/api/v1/trips/"+ids.New(), caio.token, ""), http.StatusNotFound, "trip_not_found")

		if existing["detail"] != missing["detail"] {
			t.Errorf("responses differ: %v vs %v", existing["detail"], missing["detail"])
		}
	})

	t.Run("a malformed id is a bad request", func(t *testing.T) {
		requireProblem(t, e.do(http.MethodGet, "/api/v1/trips/not-a-uuid", ana.token, ""), http.StatusBadRequest, "invalid_id")
		requireProblem(t, e.do(http.MethodGet, "/api/v1/trips/"+strings.ToUpper(id), ana.token, ""), http.StatusBadRequest, "invalid_id")
	})

	t.Run("list returns only the caller's trips", func(t *testing.T) {
		e.createTrip(t, caio)

		for account, want := range map[string]int{ana.token: 1, bia.token: 1, caio.token: 1} {
			rec := e.do(http.MethodGet, "/api/v1/trips", account, "")
			items := decode(t, rec)["items"].([]any)
			if rec.Code != http.StatusOK || len(items) != want {
				t.Errorf("status = %d, items = %d, want %d", rec.Code, len(items), want)
			}
		}
	})

	t.Run("list is an empty array, never null", func(t *testing.T) {
		dora := e.signup(t, "dora")

		rec := e.do(http.MethodGet, "/api/v1/trips", dora.token, "")

		if strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
			t.Errorf("body = %s", rec.Body)
		}
	})
}

func TestUpdateTrip(t *testing.T) {
	e := newEnv(t)
	ana, bia := e.signup(t, "ana"), e.signup(t, "bia")
	created := e.createTrip(t, ana)
	id := created["id"].(string)
	e.addMember(t, id, ana, bia, "MEMBER")
	path := "/api/v1/trips/" + id

	t.Run("requires the base version", func(t *testing.T) {
		rec := e.do(http.MethodPatch, path, ana.token, `{"name":"Renamed"}`)

		body := requireProblem(t, rec, http.StatusUnprocessableEntity, "validation_failed")
		if !strings.Contains(fmt.Sprint(body["errors"]), "baseVersion") {
			t.Errorf("errors = %v, want baseVersion", body["errors"])
		}
	})

	t.Run("members cannot edit the trip", func(t *testing.T) {
		requireProblem(t, e.do(http.MethodPatch, path, bia.token, `{"baseVersion":2,"name":"Hijacked"}`), http.StatusForbidden, "forbidden")
	})

	t.Run("partial update bumps the version", func(t *testing.T) {
		rec := e.do(http.MethodPatch, path, ana.token, `{"baseVersion":2,"name":"Renamed","currency":"usd"}`)

		body := decode(t, rec)
		if rec.Code != http.StatusOK || body["name"] != "Renamed" || body["currency"] != "USD" || body["destination"] != "Tokyo" || body["version"] != float64(3) {
			t.Errorf("status = %d, body %v", rec.Code, body)
		}
	})

	t.Run("a stale version is a conflict", func(t *testing.T) {
		requireProblem(t, e.do(http.MethodPatch, path, ana.token, `{"baseVersion":2,"name":"Late"}`), http.StatusConflict, "version_conflict")

		got := decode(t, e.do(http.MethodGet, path, ana.token, ""))
		if got["name"] != "Renamed" {
			t.Errorf("name = %v, a conflicting update must not be applied", got["name"])
		}
	})

	t.Run("validates the merged result", func(t *testing.T) {
		requireProblem(t, e.do(http.MethodPatch, path, ana.token, `{"baseVersion":3,"startDate":"2028-01-01"}`), http.StatusUnprocessableEntity, "validation_failed")
	})

	t.Run("an empty patch is rejected", func(t *testing.T) {
		requireProblem(t, e.do(http.MethodPatch, path, ana.token, `{"baseVersion":3}`), http.StatusUnprocessableEntity, "empty_patch")
	})

	t.Run("the owner cannot be replaced through a patch", func(t *testing.T) {
		requireProblem(t, e.do(http.MethodPatch, path, ana.token, `{"baseVersion":3,"ownerId":"x"}`), http.StatusBadRequest, "malformed_json")
	})
}

func TestDeleteTrip(t *testing.T) {
	e := newEnv(t)
	ana, bia := e.signup(t, "ana"), e.signup(t, "bia")
	id := e.createTrip(t, ana)["id"].(string)
	e.addMember(t, id, ana, bia, "ADMIN")
	path := "/api/v1/trips/" + id

	requireProblem(t, e.do(http.MethodDelete, path, bia.token, ""), http.StatusForbidden, "forbidden")

	if rec := e.do(http.MethodDelete, path, ana.token, ""); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status = %d, body %q; want 204 with no body", rec.Code, rec.Body)
	}

	requireProblem(t, e.do(http.MethodGet, path, ana.token, ""), http.StatusNotFound, "trip_not_found")
	requireProblem(t, e.do(http.MethodGet, path, bia.token, ""), http.StatusNotFound, "trip_not_found")
	requireProblem(t, e.do(http.MethodGet, path+"/members", ana.token, ""), http.StatusNotFound, "trip_not_found")
	if items := decode(t, e.do(http.MethodGet, "/api/v1/trips", ana.token, ""))["items"].([]any); len(items) != 0 {
		t.Errorf("a deleted trip is still listed: %v", items)
	}
}

func TestMembers(t *testing.T) {
	e := newEnv(t)
	ana, bia, caio, davi := e.signup(t, "ana"), e.signup(t, "bia"), e.signup(t, "caio"), e.signup(t, "davi")
	id := e.createTrip(t, ana)["id"].(string)
	members := "/api/v1/trips/" + id + "/members"

	t.Run("owner adds members by email", func(t *testing.T) {
		rec := e.do(http.MethodPost, members, ana.token, fmt.Sprintf(`{"email":%q,"role":"ADMIN"}`, strings.ToUpper(bia.email)))

		body := decode(t, rec)
		if rec.Code != http.StatusCreated || body["userId"] != bia.id || body["role"] != "ADMIN" || body["name"] != "bia" || body["email"] != bia.email {
			t.Errorf("status = %d, body %v", rec.Code, body)
		}
	})

	t.Run("admin adds a member but cannot add an admin", func(t *testing.T) {
		e.addMember(t, id, bia, caio, "MEMBER")

		body := fmt.Sprintf(`{"email":%q,"role":"ADMIN"}`, davi.email)
		requireProblem(t, e.do(http.MethodPost, members, bia.token, body), http.StatusForbidden, "forbidden")
	})

	t.Run("error cases", func(t *testing.T) {
		requireProblem(t, e.do(http.MethodPost, members, ana.token, `{"email":"nobody@example.com","role":"MEMBER"}`), http.StatusNotFound, "user_not_found")
		requireProblem(t, e.do(http.MethodPost, members, ana.token, fmt.Sprintf(`{"email":%q,"role":"MEMBER"}`, caio.email)), http.StatusConflict, "member_exists")
		requireProblem(t, e.do(http.MethodPost, members, ana.token, fmt.Sprintf(`{"email":%q,"role":"BOSS"}`, davi.email)), http.StatusUnprocessableEntity, "validation_failed")
		requireProblem(t, e.do(http.MethodPost, members, ana.token, fmt.Sprintf(`{"email":%q,"role":"OWNER"}`, davi.email)), http.StatusUnprocessableEntity, "owner_assignment")
		requireProblem(t, e.do(http.MethodPost, members, caio.token, fmt.Sprintf(`{"email":%q,"role":"VIEWER"}`, davi.email)), http.StatusForbidden, "forbidden")
		requireProblem(t, e.do(http.MethodPost, members, davi.token, fmt.Sprintf(`{"email":%q,"role":"VIEWER"}`, davi.email)), http.StatusNotFound, "trip_not_found")
	})

	t.Run("any member lists them, owner first", func(t *testing.T) {
		rec := e.do(http.MethodGet, members, caio.token, "")

		items := decode(t, rec)["items"].([]any)
		var order []string
		for _, item := range items {
			order = append(order, item.(map[string]any)["role"].(string))
		}
		if rec.Code != http.StatusOK || strings.Join(order, ",") != "OWNER,ADMIN,MEMBER" {
			t.Errorf("status = %d, roles = %v", rec.Code, order)
		}
		if strings.Contains(rec.Body.String(), "password") {
			t.Errorf("members response mentions passwords: %s", rec.Body)
		}
		requireProblem(t, e.do(http.MethodGet, members, davi.token, ""), http.StatusNotFound, "trip_not_found")
	})

	t.Run("owner changes a role", func(t *testing.T) {
		rec := e.do(http.MethodPatch, members+"/"+caio.id, ana.token, `{"role":"VIEWER"}`)

		if body := decode(t, rec); rec.Code != http.StatusOK || body["role"] != "VIEWER" || body["email"] != caio.email {
			t.Errorf("status = %d, body %v", rec.Code, body)
		}
		requireProblem(t, e.do(http.MethodPost, "/api/v1/trips/"+id+"/members", caio.token, fmt.Sprintf(`{"email":%q,"role":"VIEWER"}`, davi.email)), http.StatusForbidden, "forbidden")
	})

	t.Run("role change rules", func(t *testing.T) {
		requireProblem(t, e.do(http.MethodPatch, members+"/"+ana.id, bia.token, `{"role":"MEMBER"}`), http.StatusForbidden, "forbidden")
		requireProblem(t, e.do(http.MethodPatch, members+"/"+ana.id, ana.token, `{"role":"ADMIN"}`), http.StatusUnprocessableEntity, "owner_locked")
		requireProblem(t, e.do(http.MethodPatch, members+"/"+caio.id, ana.token, `{"role":"OWNER"}`), http.StatusUnprocessableEntity, "owner_assignment")
		requireProblem(t, e.do(http.MethodPatch, members+"/"+davi.id, ana.token, `{"role":"MEMBER"}`), http.StatusNotFound, "member_not_found")
		requireProblem(t, e.do(http.MethodPatch, members+"/not-a-uuid", ana.token, `{"role":"MEMBER"}`), http.StatusBadRequest, "invalid_id")
	})

	t.Run("removing and leaving", func(t *testing.T) {
		requireProblem(t, e.do(http.MethodDelete, members+"/"+ana.id, bia.token, ""), http.StatusForbidden, "forbidden")
		requireProblem(t, e.do(http.MethodDelete, members+"/"+ana.id, ana.token, ""), http.StatusUnprocessableEntity, "owner_locked")

		if rec := e.do(http.MethodDelete, members+"/"+caio.id, bia.token, ""); rec.Code != http.StatusNoContent {
			t.Errorf("admin removing a viewer: status = %d", rec.Code)
		}
		requireProblem(t, e.do(http.MethodGet, "/api/v1/trips/"+id, caio.token, ""), http.StatusNotFound, "trip_not_found")

		if rec := e.do(http.MethodDelete, members+"/"+bia.id, bia.token, ""); rec.Code != http.StatusNoContent {
			t.Errorf("admin leaving: status = %d", rec.Code)
		}
		requireProblem(t, e.do(http.MethodGet, "/api/v1/trips/"+id, bia.token, ""), http.StatusNotFound, "trip_not_found")
	})
}

func TestTransferOwnership(t *testing.T) {
	e := newEnv(t)
	ana, bia, caio := e.signup(t, "ana"), e.signup(t, "bia"), e.signup(t, "caio")
	id := e.createTrip(t, ana)["id"].(string)
	e.addMember(t, id, ana, bia, "MEMBER")
	path := "/api/v1/trips/" + id + "/transfer-ownership"

	requireProblem(t, e.do(http.MethodPost, path, bia.token, fmt.Sprintf(`{"userId":%q}`, bia.id)), http.StatusForbidden, "forbidden")
	requireProblem(t, e.do(http.MethodPost, path, ana.token, fmt.Sprintf(`{"userId":%q}`, caio.id)), http.StatusNotFound, "member_not_found")
	requireProblem(t, e.do(http.MethodPost, path, ana.token, `{"userId":"nope"}`), http.StatusUnprocessableEntity, "validation_failed")

	rec := e.do(http.MethodPost, path, ana.token, fmt.Sprintf(`{"userId":%q}`, bia.id))
	body := decode(t, rec)
	if rec.Code != http.StatusOK || body["ownerId"] != bia.id || body["myRole"] != "ADMIN" {
		t.Fatalf("status = %d, body %v", rec.Code, body)
	}

	requireProblem(t, e.do(http.MethodDelete, "/api/v1/trips/"+id, ana.token, ""), http.StatusForbidden, "forbidden")
	if rec := e.do(http.MethodGet, "/api/v1/trips/"+id, bia.token, ""); decode(t, rec)["myRole"] != "OWNER" {
		t.Errorf("the new owner sees role %v", decode(t, rec)["myRole"])
	}
}

func TestOneUsersTripsNeverLeakToAnother(t *testing.T) {
	e := newEnv(t)
	ana, bia := e.signup(t, "ana"), e.signup(t, "bia")
	id := e.createTrip(t, ana)["id"].(string)
	path := "/api/v1/trips/" + id

	for _, route := range []struct{ method, path, body string }{
		{"GET", path, ""},
		{"PATCH", path, `{"baseVersion":1,"name":"Mine now"}`},
		{"DELETE", path, ""},
		{"GET", path + "/members", ""},
		{"POST", path + "/members", fmt.Sprintf(`{"email":%q,"role":"OWNER"}`, bia.email)},
		{"PATCH", path + "/members/" + ana.id, `{"role":"VIEWER"}`},
		{"DELETE", path + "/members/" + ana.id, ""},
		{"POST", path + "/transfer-ownership", fmt.Sprintf(`{"userId":%q}`, bia.id)},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			requireProblem(t, e.do(route.method, route.path, bia.token, route.body), http.StatusNotFound, "trip_not_found")
		})
	}

	got := decode(t, e.do(http.MethodGet, path, ana.token, ""))
	if got["name"] != "Japan 2027" || got["ownerId"] != ana.id || got["version"] != float64(1) {
		t.Errorf("the trip was changed by an outsider: %v", got)
	}
}

func TestCapabilitiesReflectTheCallersRole(t *testing.T) {
	e := newEnv(t)
	ana, bia, caio, davi := e.signup(t, "ana"), e.signup(t, "bia"), e.signup(t, "caio"), e.signup(t, "davi")
	id := e.createTrip(t, ana)["id"].(string)
	e.addMember(t, id, ana, bia, "ADMIN")
	e.addMember(t, id, ana, caio, "MEMBER")
	e.addMember(t, id, ana, davi, "VIEWER")

	capsOf := func(tok string) map[string]any {
		return decode(t, e.do(http.MethodGet, "/api/v1/trips/"+id, tok, ""))["capabilities"].(map[string]any)
	}

	wants := map[string]struct {
		token string
		caps  map[string]bool
	}{
		"owner":  {ana.token, map[string]bool{"updateTrip": true, "manageMembers": true, "writeContent": true, "deleteTrip": true, "transferOwnership": true, "leave": false}},
		"admin":  {bia.token, map[string]bool{"updateTrip": true, "manageMembers": true, "writeContent": true, "deleteTrip": false, "transferOwnership": false, "leave": true}},
		"member": {caio.token, map[string]bool{"updateTrip": false, "manageMembers": false, "writeContent": true, "deleteTrip": false, "transferOwnership": false, "leave": true}},
		"viewer": {davi.token, map[string]bool{"updateTrip": false, "manageMembers": false, "writeContent": false, "deleteTrip": false, "transferOwnership": false, "leave": true}},
	}
	for role, want := range wants {
		got := capsOf(want.token)
		for key, value := range want.caps {
			if got[key] != value {
				t.Errorf("%s: capabilities.%s = %v, want %v", role, key, got[key], value)
			}
		}
	}

	addable := func(tok string) string { return fmt.Sprint(capsOf(tok)["addableRoles"]) }
	if addable(ana.token) != "[ADMIN MEMBER VIEWER]" || addable(bia.token) != "[MEMBER VIEWER]" || addable(caio.token) != "[]" || addable(davi.token) != "[]" {
		t.Errorf("addableRoles: owner=%s admin=%s member=%s viewer=%s", addable(ana.token), addable(bia.token), addable(caio.token), addable(davi.token))
	}
}

func TestMemberCapabilitiesDependOnWhoIsAsking(t *testing.T) {
	e := newEnv(t)
	ana, bia, caio := e.signup(t, "ana"), e.signup(t, "bia"), e.signup(t, "caio")
	id := e.createTrip(t, ana)["id"].(string)
	e.addMember(t, id, ana, bia, "ADMIN")
	e.addMember(t, id, ana, caio, "MEMBER")

	summary := func(tok string) map[string]string {
		out := map[string]string{}
		items := decode(t, e.do(http.MethodGet, "/api/v1/trips/"+id+"/members", tok, ""))["items"].([]any)
		for _, item := range items {
			m := item.(map[string]any)
			caps := m["capabilities"].(map[string]any)
			out[m["name"].(string)] = fmt.Sprintf("%v remove=%v", caps["assignableRoles"], caps["canRemove"])
		}
		return out
	}

	if got := summary(ana.token); got["ana"] != "[] remove=false" || got["bia"] != "[MEMBER VIEWER] remove=true" || got["caio"] != "[ADMIN VIEWER] remove=true" {
		t.Errorf("as owner: %v", got)
	}
	if got := summary(bia.token); got["ana"] != "[] remove=false" || got["bia"] != "[] remove=true" || got["caio"] != "[VIEWER] remove=true" {
		t.Errorf("as admin: %v", got)
	}
	if got := summary(caio.token); got["ana"] != "[] remove=false" || got["bia"] != "[] remove=false" || got["caio"] != "[] remove=true" {
		t.Errorf("as member: %v", got)
	}
}
