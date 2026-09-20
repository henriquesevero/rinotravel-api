//go:build integration

package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rinotravel-api/internal/app"
	"rinotravel-api/internal/platform/config"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/platform/mongodb/mongotest"
	"rinotravel-api/internal/server"
)

type api struct {
	t       *testing.T
	handler http.Handler
	token   string
}

func (a *api) do(method, path, body string) (int, map[string]any) {
	a.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (a *api) must(method, path, body string, want int) map[string]any {
	a.t.Helper()
	code, out := a.do(method, path, body)
	if code != want {
		a.t.Fatalf("%s %s = %d, want %d: %v", method, path, code, want, out)
	}
	return out
}

func newApp(t *testing.T) *api {
	t.Helper()
	ctx := context.Background()

	// Signed file links point at the API's own address, so the server must exist before the app is built.
	a := &api{t: t}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)

	cfg := config.Config{
		Env: config.Development, AuthRateLimit: 100000, RegistrationCode: "dev-registration-code",
		StorageSigningSecret: strings.Repeat("s", 32), APIPublicURL: srv.URL,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	modules, err := app.Build(ctx, app.Deps{Logger: logger, Config: cfg, DB: mongotest.Database(t)})
	if err != nil {
		t.Fatal(err)
	}
	a.handler = server.NewHandler(server.Options{Logger: logger, Modules: modules})
	return a
}

func TestWholeApplicationAgainstRealInfrastructure(t *testing.T) {
	a := newApp(t)
	reg := a.must("POST", "/api/v1/auth/register", `{"email":"ana@example.com","name":"Ana","password":"correct horse battery","registrationCode":"dev-registration-code"}`, 201)
	a.token = reg["token"].(string)

	tripID := a.must("POST", "/api/v1/trips", `{"name":"Japan","destination":"Tokyo","startDate":"2027-04-01","endDate":"2027-04-15","timezone":"Asia/Tokyo","currency":"JPY"}`, 201)["id"].(string)
	base := "/api/v1/trips/" + tripID

	day := a.must("POST", base+"/itinerary-days", `{"date":"2027-04-03","title":"Asakusa"}`, 201)
	a.must("POST", base+"/itinerary-days", `{"date":"2027-04-03"}`, 409)
	place := a.must("POST", base+"/places", `{"name":"Senso-ji","category":"ATTRACTION","location":{"name":"Senso-ji","latitude":35.7148,"longitude":139.7967},"estimatedCost":{"amount":500,"currency":"JPY"}}`, 201)
	item := a.must("POST", base+"/itinerary-items/from-place", fmt.Sprintf(`{"placeId":%q,"dayId":%q,"start":{"dateTime":"2027-04-03T09:00"}}`, place["id"], day["id"]), 201)
	if item["title"] != "Senso-ji" || item["estimatedCost"].(map[string]any)["amount"] != float64(500) || item["location"].(map[string]any)["latitude"] != 35.7148 {
		t.Errorf("place round trip through MongoDB: %v", item)
	}
	a.must("POST", base+"/restaurants", `{"name":"Sushi Dai","status":"RESERVED","reservationAt":{"dateTime":"2027-04-03T19:00"},"reservationCode":"C1","desiredDishes":["omakase"]}`, 201)
	a.must("POST", base+"/flights", `{"flightNumber":"LA8084","departureAirport":"GRU","arrivalAirport":"LIS","departure":{"dateTime":"2027-04-01T22:00","timezone":"America/Sao_Paulo"},"arrival":{"dateTime":"2027-04-02T18:30","timezone":"Europe/Lisbon"}}`, 201)
	a.must("POST", base+"/hotels", `{"name":"Park Hyatt","checkIn":{"dateTime":"2027-04-02T15:00"},"checkOut":{"dateTime":"2027-04-05T11:00"}}`, 201)
	transfer := a.must("POST", base+"/transfers", `{"legs":[{"mode":"TRAIN","origin":{"name":"A"},"destination":{"name":"B"},"departure":{"dateTime":"2027-04-03T08:00"},"arrival":{"dateTime":"2027-04-03T08:40"},"cost":{"amount":300,"currency":"JPY"}}]}`, 201)
	if transfer["durationMinutes"] != float64(40) || transfer["totalCost"].(map[string]any)["amount"] != float64(300) {
		t.Errorf("transfer round trip: %v", transfer)
	}

	timeline := a.must("GET", base+"/itinerary", "", 200)["days"].([]any)
	if len(timeline) < 4 {
		t.Errorf("timeline has %d days, want the trip's day plus flight, hotel and transfer dates: %v", len(timeline), timeline)
	}

	t.Run("sync uses real transactions and a durable mutation log", func(t *testing.T) {
		newID, m1, m2 := ids.New(), ids.New(), ids.New()
		push := func(mutation string) map[string]any {
			return a.must("POST", base+"/sync", `{"mutations":[`+mutation+`]}`, 200)["results"].([]any)[0].(map[string]any)
		}
		create := fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_item","entityId":%q,"operation":"CREATE","payload":{"dayId":%q,"title":"Offline","category":"OTHER"}}`, m1, newID, day["id"])

		if r := push(create); r["status"] != "applied" {
			t.Fatalf("first push = %v", r)
		}
		if r := push(create); r["status"] != "duplicate" {
			t.Errorf("retry = %v, want a duplicate answered from the log", r)
		}
		if _, out := a.do("GET", base+"/itinerary-items/"+newID, ""); out["title"] != "Offline" {
			t.Errorf("stored item = %v", out)
		}
		stale := push(fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_item","entityId":%q,"operation":"UPDATE","baseVersion":9,"payload":{"title":"x"}}`, m2, newID))
		if stale["status"] != "conflict" || stale["record"] == nil {
			t.Errorf("stale update = %v", stale)
		}

		pull := a.must("GET", base+"/sync", "", 200)
		seen := map[string]bool{}
		for _, c := range pull["changes"].([]any) {
			seen[c.(map[string]any)["entity"].(string)] = true
		}
		for _, entity := range []string{"trip", "itinerary_day", "itinerary_item", "place", "restaurant", "flight", "hotel", "transfer"} {
			if !seen[entity] {
				t.Errorf("the initial sync is missing %s (got %v)", entity, seen)
			}
		}
	})

	t.Run("documents are stored in MongoDB GridFS behind signed links", func(t *testing.T) {
		content := []byte("%PDF-1.4 fake ticket content")
		sum := sha256.Sum256(content)
		checksum := hex.EncodeToString(sum[:])
		declare := func(name string) map[string]any {
			return a.must("POST", base+"/documents", fmt.Sprintf(`{"name":%q,"type":"TICKET","fileName":"ticket.pdf","mimeType":"application/pdf","size":%d,"checksum":%q}`, name, len(content), checksum), 201)
		}
		put := func(upload map[string]any, data []byte) (int, string) {
			req, _ := http.NewRequest(upload["method"].(string), upload["url"].(string), bytes.NewReader(data))
			req.Header.Set("Content-Type", "application/pdf")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(body)
		}

		first := declare("Ticket")
		id, upload := first["document"].(map[string]any)["id"].(string), first["upload"].(map[string]any)
		a.must("POST", base+"/documents/"+id+"/complete", "", 422)

		if code, body := put(upload, content); code != http.StatusNoContent {
			t.Fatalf("upload = %d %s", code, body)
		}
		done := a.must("POST", base+"/documents/"+id+"/complete", "", 200)
		if done["status"] != "READY" || done["checksum"] != checksum || done["size"] != float64(len(content)) {
			t.Errorf("complete = %v", done)
		}

		dl := a.must("GET", base+"/documents/"+id+"/download", "", 200)["download"].(map[string]any)
		resp, err := http.Get(dl["url"].(string))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !bytes.Equal(got, content) || resp.Header.Get("Content-Type") != "application/pdf" || !strings.Contains(resp.Header.Get("Content-Disposition"), `filename="ticket.pdf"`) ||
			resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Cache-Control") != "private, no-store" {
			t.Errorf("download = %q, headers %v", got, resp.Header)
		}

		second := declare("Tampered")
		tampered := bytes.Repeat([]byte("x"), len(content))
		if code, _ := put(second["upload"].(map[string]any), tampered); code != http.StatusUnprocessableEntity {
			t.Errorf("content that does not match the declared checksum = %d, want 422", code)
		}
		secondID := second["document"].(map[string]any)["id"].(string)
		a.must("POST", base+"/documents/"+secondID+"/complete", "", 422)
		if code, _ := put(second["upload"].(map[string]any), content[:5]); code != http.StatusBadRequest {
			t.Errorf("wrong length = %d, want 400", code)
		}
		if code, _ := put(second["upload"].(map[string]any), content); code != http.StatusNoContent {
			t.Errorf("a valid retry after a rejected upload = %d", code)
		}

		forged := strings.Replace(upload["url"].(string), "/api/v1/storage/", "/api/v1/storage/x", 1)
		if r, err := http.Get(forged); err == nil {
			r.Body.Close()
			if r.StatusCode != http.StatusForbidden {
				t.Errorf("a forged link = %d, want 403", r.StatusCode)
			}
		}
		wrongOp, _ := http.NewRequest(http.MethodPut, dl["url"].(string), bytes.NewReader(content))
		if r, err := http.DefaultClient.Do(wrongOp); err != nil || r.StatusCode != http.StatusForbidden {
			t.Errorf("a download link must not accept uploads (err %v)", err)
		}

		a.must("DELETE", base+"/documents/"+id, "", 204)
		if r, err := http.Get(dl["url"].(string)); err != nil || r.StatusCode != http.StatusNotFound {
			t.Errorf("the stored file must be gone after the document is deleted")
		}
	})
}
