package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"rinotravel-api/internal/apitest"
	"rinotravel-api/internal/itinerary"
	"rinotravel-api/internal/itinerary/httpapi"
	"rinotravel-api/internal/resource/resourcetest"
	"rinotravel-api/internal/server"
)

func newEnv(t *testing.T) *apitest.Env {
	t.Helper()
	return apitest.New(t, func(e apitest.Env) []server.Module {
		days := resourcetest.New(itinerary.DayBase).WithUnique(func(a, b itinerary.Day) bool { return a.Date == b.Date })
		items := resourcetest.New(itinerary.ItemBase)
		return []server.Module{httpapi.New(httpapi.Deps{
			Logger:   e.Logger,
			Guard:    e.Guard,
			Days:     itinerary.NewDays(days, items, e.Authz),
			Items:    itinerary.NewItems(items, days, e.Authz),
			Timeline: itinerary.NewTimeline(days, items, e.Authz),
		})}
	})
}

func daysPath(tripID string) string  { return "/api/v1/trips/" + tripID + "/itinerary-days" }
func itemsPath(tripID string) string { return "/api/v1/trips/" + tripID + "/itinerary-items" }

func TestAllRoutesRequireAuthentication(t *testing.T) {
	e := newEnv(t)
	id := "01a0bc21-eec6-7242-ab68-ed9d57f5ac30"
	for _, r := range []struct{ method, path string }{
		{"POST", daysPath(id)}, {"GET", daysPath(id)}, {"GET", daysPath(id) + "/" + id}, {"PATCH", daysPath(id) + "/" + id}, {"DELETE", daysPath(id) + "/" + id},
		{"POST", itemsPath(id)}, {"GET", itemsPath(id)}, {"GET", itemsPath(id) + "/" + id}, {"PATCH", itemsPath(id) + "/" + id}, {"DELETE", itemsPath(id) + "/" + id},
		{"GET", "/api/v1/trips/" + id + "/itinerary"},
	} {
		apitest.RequireProblem(t, e.Do(r.method, r.path, "", "{}"), http.StatusUnauthorized, "unauthenticated")
	}
}

func TestDayAndItemLifecycle(t *testing.T) {
	e := newEnv(t)
	ana := e.Signup(t, "Ana")
	tripID := e.CreateTrip(t, ana)

	day := e.Create(t, daysPath(tripID), ana.Token, `{"date":"2027-04-03","title":"Asakusa"}`)
	dayID := day["id"].(string)
	if day["date"] != "2027-04-03" || day["title"] != "Asakusa" || day["version"] != float64(1) || day["tripId"] != tripID {
		t.Fatalf("unexpected day: %v", day)
	}
	apitest.RequireProblem(t, e.Do("POST", daysPath(tripID), ana.Token, `{"date":"2027-04-03"}`), http.StatusConflict, "day_exists")
	body := apitest.RequireProblem(t, e.Do("POST", daysPath(tripID), ana.Token, `{"date":"2028-01-01"}`), http.StatusUnprocessableEntity, "validation_failed")
	if !apitest.Fields(body)["date"] {
		t.Errorf("errors = %v", body["errors"])
	}

	item := e.Create(t, itemsPath(tripID), ana.Token, fmt.Sprintf(
		`{"dayId":%q,"title":"Senso-ji","category":"ATTRACTION","start":{"dateTime":"2027-04-03T09:30"},"end":{"dateTime":"2027-04-03T11:00"},`+
			`"location":{"name":"Senso-ji","latitude":35.7148,"longitude":139.7967},"estimatedCost":{"amount":500,"currency":"JPY"}}`, dayID))
	itemID := item["id"].(string)
	start, _ := item["start"].(map[string]any)
	if item["status"] != "PLANNED" || item["durationMinutes"] != float64(90) || start["timezone"] != "Asia/Tokyo" || start["dateTime"] != "2027-04-03T09:30" {
		t.Errorf("unexpected item: %v", item)
	}

	patched := e.Do("PATCH", itemsPath(tripID)+"/"+itemID, ana.Token, `{"baseVersion":1,"status":"CONFIRMED","end":null,"estimatedDurationMinutes":45}`)
	got := apitest.Decode(t, patched)
	if patched.Code != http.StatusOK || got["status"] != "CONFIRMED" || got["end"] != nil || got["durationMinutes"] != float64(45) || got["version"] != float64(2) {
		t.Errorf("PATCH status = %d, body %s", patched.Code, patched.Body)
	}
	apitest.RequireProblem(t, e.Do("PATCH", itemsPath(tripID)+"/"+itemID, ana.Token, `{"baseVersion":1,"title":"Late"}`), http.StatusConflict, "version_conflict")
	apitest.RequireProblem(t, e.Do("PATCH", itemsPath(tripID)+"/"+itemID, ana.Token, `{"title":"No version"}`), http.StatusUnprocessableEntity, "validation_failed")
	apitest.RequireProblem(t, e.Do("PATCH", itemsPath(tripID)+"/"+itemID, ana.Token, `{"baseVersion":2,"unknownField":1}`), http.StatusBadRequest, "malformed_json")

	apitest.RequireProblem(t, e.Do("DELETE", daysPath(tripID)+"/"+dayID, ana.Token, ""), http.StatusConflict, "day_not_empty")
	if rec := e.Do("DELETE", itemsPath(tripID)+"/"+itemID, ana.Token, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE item status = %d", rec.Code)
	}
	apitest.RequireProblem(t, e.Do("GET", itemsPath(tripID)+"/"+itemID, ana.Token, ""), http.StatusNotFound, "itinerary_item_not_found")
	if rec := e.Do("DELETE", daysPath(tripID)+"/"+dayID, ana.Token, ""); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE empty day status = %d", rec.Code)
	}
}

func TestTimelineEndpoint(t *testing.T) {
	e := newEnv(t)
	ana := e.Signup(t, "Ana")
	tripID := e.CreateTrip(t, ana)
	dayID := e.Create(t, daysPath(tripID), ana.Token, `{"date":"2027-04-03"}`)["id"].(string)
	for _, body := range []string{
		fmt.Sprintf(`{"dayId":%q,"title":"Dinner","category":"RESTAURANT","start":{"dateTime":"2027-04-03T19:00"}}`, dayID),
		fmt.Sprintf(`{"dayId":%q,"title":"Breakfast","category":"RESTAURANT","start":{"dateTime":"2027-04-03T08:00"}}`, dayID),
		fmt.Sprintf(`{"dayId":%q,"title":"Wander","category":"FREE_TIME"}`, dayID),
	} {
		e.Create(t, itemsPath(tripID), ana.Token, body)
	}

	rec := e.Do("GET", "/api/v1/trips/"+tripID+"/itinerary", ana.Token, "")

	body := apitest.Decode(t, rec)
	days := body["days"].([]any)
	entries := days[0].(map[string]any)["entries"].([]any)
	var titles []string
	for _, entry := range entries {
		titles = append(titles, entry.(map[string]any)["title"].(string))
	}
	if rec.Code != http.StatusOK || fmt.Sprint(titles) != "[Breakfast Dinner Wander]" || days[0].(map[string]any)["day"] == nil {
		t.Errorf("status = %d, titles = %v, body %s", rec.Code, titles, rec.Body)
	}
}

func TestPermissionsAndIsolation(t *testing.T) {
	e := newEnv(t)
	ana, bia, caio := e.Signup(t, "Ana"), e.Signup(t, "Bia"), e.Signup(t, "Caio")
	tripID := e.CreateTrip(t, ana)
	e.AddMember(t, tripID, ana, bia, "VIEWER")
	dayID := e.Create(t, daysPath(tripID), ana.Token, `{"date":"2027-04-03"}`)["id"].(string)
	itemID := e.Create(t, itemsPath(tripID), ana.Token, fmt.Sprintf(`{"dayId":%q,"title":"x","category":"OTHER"}`, dayID))["id"].(string)

	t.Run("a viewer can read everything and write nothing", func(t *testing.T) {
		for _, path := range []string{daysPath(tripID), itemsPath(tripID), itemsPath(tripID) + "/" + itemID, "/api/v1/trips/" + tripID + "/itinerary"} {
			if rec := e.Do("GET", path, bia.Token, ""); rec.Code != http.StatusOK {
				t.Errorf("viewer GET %s = %d", path, rec.Code)
			}
		}
		apitest.RequireProblem(t, e.Do("POST", daysPath(tripID), bia.Token, `{"date":"2027-04-04"}`), http.StatusForbidden, "forbidden")
		apitest.RequireProblem(t, e.Do("PATCH", itemsPath(tripID)+"/"+itemID, bia.Token, `{"baseVersion":1,"title":"y"}`), http.StatusForbidden, "forbidden")
		apitest.RequireProblem(t, e.Do("DELETE", itemsPath(tripID)+"/"+itemID, bia.Token, ""), http.StatusForbidden, "forbidden")
	})

	t.Run("an outsider cannot see that the trip or its content exists", func(t *testing.T) {
		for _, r := range []struct{ method, path, body string }{
			{"GET", daysPath(tripID), ""}, {"POST", daysPath(tripID), `{"date":"2027-04-04"}`},
			{"GET", itemsPath(tripID) + "/" + itemID, ""}, {"DELETE", itemsPath(tripID) + "/" + itemID, ""},
			{"GET", "/api/v1/trips/" + tripID + "/itinerary", ""},
		} {
			apitest.RequireProblem(t, e.Do(r.method, r.path, caio.Token, r.body), http.StatusNotFound, "trip_not_found")
		}
	})

	t.Run("an item id cannot be read through another trip", func(t *testing.T) {
		otherTrip := e.CreateTrip(t, caio)

		apitest.RequireProblem(t, e.Do("GET", itemsPath(otherTrip)+"/"+itemID, caio.Token, ""), http.StatusNotFound, "itinerary_item_not_found")
		apitest.RequireProblem(t, e.Do("DELETE", itemsPath(otherTrip)+"/"+itemID, caio.Token, ""), http.StatusNotFound, "itinerary_item_not_found")
	})

	t.Run("malformed ids are rejected", func(t *testing.T) {
		apitest.RequireProblem(t, e.Do("GET", itemsPath(tripID)+"/not-a-uuid", ana.Token, ""), http.StatusBadRequest, "invalid_id")
		apitest.RequireProblem(t, e.Do("GET", itemsPath("nope"), ana.Token, ""), http.StatusBadRequest, "invalid_id")
	})
}
