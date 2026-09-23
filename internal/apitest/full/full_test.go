package full_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"rinotravel-api/internal/apitest"
	"rinotravel-api/internal/apitest/full"
	"rinotravel-api/internal/google"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/place"
	"rinotravel-api/internal/routing"
)

const checksum = "a3f5c0d1e2b4968778695a4b3c2d1e0f9a8b7c6d5e4f30211203f4e5d6c7b8a9"

type world struct {
	*full.Stack
	ana, bia, caio apitest.Account
	trip           string
	base           string
}

func newWorld(t *testing.T) world {
	t.Helper()
	s := full.New(t)
	w := world{Stack: s, ana: s.Signup(t, "Ana"), bia: s.Signup(t, "Bia"), caio: s.Signup(t, "Caio")}
	w.trip = s.CreateTrip(t, w.ana)
	s.AddMember(t, w.trip, w.ana, w.bia, "VIEWER")
	w.base = "/api/v1/trips/" + w.trip
	return w
}

func (w world) post(t *testing.T, path string, who apitest.Account, body string) map[string]any {
	t.Helper()
	return w.Create(t, w.base+path, who.Token, body)
}

func (w world) get(t *testing.T, path string, who apitest.Account) map[string]any {
	t.Helper()
	rec := w.Do("GET", w.base+path, who.Token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
	}
	return apitest.Decode(t, rec)
}

func (w world) day(t *testing.T, date string) string {
	t.Helper()
	return w.post(t, "/itinerary-days", w.ana, fmt.Sprintf(`{"date":%q}`, date))["id"].(string)
}

func (w world) problem(t *testing.T, method, path string, who apitest.Account, body string, status int, code string) map[string]any {
	t.Helper()
	return apitest.RequireProblem(t, w.Do(method, w.base+path, who.Token, body), status, code)
}

func TestPlacesRestaurantsAndScheduling(t *testing.T) {
	w := newWorld(t)
	placeID := w.post(t, "/places", w.ana, `{"name":"Senso-ji","category":"ATTRACTION","priority":"HIGH","location":{"name":"Senso-ji","latitude":35.7148,"longitude":139.7967},"estimatedDurationMinutes":90,"estimatedCost":{"amount":500,"currency":"JPY"},"externalId":"ChIJ123"}`)["id"].(string)
	dayID := w.day(t, "2027-04-03")

	item := w.post(t, "/itinerary-items/from-place", w.ana, fmt.Sprintf(`{"placeId":%q,"dayId":%q,"start":{"dateTime":"2027-04-03T09:00"}}`, placeID, dayID))
	if item["title"] != "Senso-ji" || item["placeId"] != placeID || item["category"] != "ATTRACTION" || item["estimatedDurationMinutes"] != float64(90) ||
		item["location"].(map[string]any)["latitude"] != 35.7148 || item["estimatedCost"].(map[string]any)["amount"] != float64(500) {
		t.Errorf("the item was not built from the place: %v", item)
	}
	w.problem(t, "POST", "/itinerary-items/from-place", w.ana, fmt.Sprintf(`{"placeId":"%s","dayId":%q}`, "01a0bc21-eec6-7242-ab68-ed9d57f5ac30", dayID), 404, "place_not_found")
	w.problem(t, "POST", "/places", w.bia, `{"name":"x","category":"OTHER"}`, 403, "forbidden")
	body := w.problem(t, "POST", "/places", w.ana, `{"name":"","category":"CASTLE","priority":"URGENT"}`, 422, "validation_failed")
	if f := apitest.Fields(body); !f["name"] || !f["category"] || !f["priority"] {
		t.Errorf("fields = %v", f)
	}

	w.problem(t, "POST", "/restaurants", w.ana, `{"name":"Sushi Dai","status":"RESERVED"}`, 422, "validation_failed")
	restaurant := w.post(t, "/restaurants", w.ana, `{"name":"Sushi Dai","cuisine":"Sushi","status":"RESERVED","reservationAt":{"dateTime":"2027-04-03T19:00"},"reservationCode":"ABC123","desiredDishes":["omakase"]}`)
	if restaurant["reservationCode"] != "ABC123" {
		t.Errorf("the writer must see the code: %v", restaurant)
	}
	viewerView := w.get(t, "/restaurants/"+restaurant["id"].(string), w.bia)
	if _, leaked := viewerView["reservationCode"]; leaked || viewerView["name"] != "Sushi Dai" {
		t.Errorf("a viewer must not see the reservation code: %v", viewerView)
	}

	day := w.get(t, "/itinerary", w.bia)["days"].([]any)[0].(map[string]any)
	kinds := map[string]bool{}
	for _, e := range day["entries"].([]any) {
		kinds[e.(map[string]any)["kind"].(string)] = true
	}
	if !kinds["itinerary_item"] || !kinds["restaurant_reservation"] {
		t.Errorf("timeline kinds = %v", kinds)
	}
}

func TestFlightsAndHotels(t *testing.T) {
	w := newWorld(t)
	flight := w.post(t, "/flights", w.ana, `{"airline":"LATAM","flightNumber":"la 8084","departureAirport":"gru","arrivalAirport":"LIS","departure":{"dateTime":"2027-04-01T22:00","timezone":"America/Sao_Paulo"},"arrival":{"dateTime":"2027-04-02T18:30","timezone":"Europe/Lisbon"},"bookingCode":"XYZ789","seat":"12A"}`)
	if flight["flightNumber"] != "LA8084" || flight["departureAirport"] != "GRU" || flight["durationMinutes"] != float64(990) || flight["bookingCode"] != "XYZ789" {
		t.Errorf("flight = %v", flight)
	}
	if _, leaked := w.get(t, "/flights/"+flight["id"].(string), w.bia)["bookingCode"]; leaked {
		t.Error("a viewer must not see the booking code")
	}

	for name, body := range map[string]string{
		"no timezone":     `{"flightNumber":"LA1","departureAirport":"GRU","arrivalAirport":"LIS","departure":{"dateTime":"2027-04-01T22:00"},"arrival":{"dateTime":"2027-04-02T10:00","timezone":"Europe/Lisbon"}}`,
		"arrives first":   `{"flightNumber":"LA1","departureAirport":"GRU","arrivalAirport":"LIS","departure":{"dateTime":"2027-04-02T10:00","timezone":"Europe/Lisbon"},"arrival":{"dateTime":"2027-04-02T09:00","timezone":"Europe/Lisbon"}}`,
		"bad airport":     `{"flightNumber":"LA1","departureAirport":"SAO","arrivalAirport":"LI","departure":{"dateTime":"2027-04-01T22:00","timezone":"UTC"},"arrival":{"dateTime":"2027-04-02T10:00","timezone":"UTC"}}`,
		"same airport":    `{"flightNumber":"LA1","departureAirport":"GRU","arrivalAirport":"GRU","departure":{"dateTime":"2027-04-01T22:00","timezone":"UTC"},"arrival":{"dateTime":"2027-04-02T10:00","timezone":"UTC"}}`,
		"bad flight code": `{"flightNumber":"???","departureAirport":"GRU","arrivalAirport":"LIS","departure":{"dateTime":"2027-04-01T22:00","timezone":"UTC"},"arrival":{"dateTime":"2027-04-02T10:00","timezone":"UTC"}}`,
	} {
		w.problem(t, "POST", "/flights", w.ana, body, 422, "validation_failed")
		_ = name
	}

	hotel := w.post(t, "/hotels", w.ana, `{"name":"Park Hyatt","checkIn":{"dateTime":"2027-04-02T15:00"},"checkOut":{"dateTime":"2027-04-05T11:00"},"confirmationCode":"CONF1","bookingUrl":"https://example.com/b","contactPhone":"+81 3 5322 1234"}`)
	if hotel["checkIn"].(map[string]any)["timezone"] != "Asia/Tokyo" || hotel["confirmationCode"] != "CONF1" {
		t.Errorf("hotel = %v", hotel)
	}
	if _, leaked := w.get(t, "/hotels/"+hotel["id"].(string), w.bia)["confirmationCode"]; leaked {
		t.Error("a viewer must not see the confirmation code")
	}
	for _, body := range []string{
		`{"name":"H","checkIn":{"dateTime":"2027-04-05T15:00"},"checkOut":{"dateTime":"2027-04-02T11:00"}}`,
		`{"name":"H","checkIn":{"dateTime":"2027-04-02T15:00"},"checkOut":{"dateTime":"2027-04-05T11:00"},"bookingUrl":"javascript:alert(1)"}`,
		`{"name":"H","checkIn":{"dateTime":"2027-04-02T15:00"},"checkOut":{"dateTime":"2027-04-05T11:00"},"contactPhone":"call me"}`,
	} {
		w.problem(t, "POST", "/hotels", w.ana, body, 422, "validation_failed")
	}

	days := w.get(t, "/itinerary", w.ana)["days"].([]any)
	var dates []string
	for _, d := range days {
		dates = append(dates, d.(map[string]any)["date"].(string))
	}
	if fmt.Sprint(dates) != "[2027-04-01 2027-04-02 2027-04-05]" {
		t.Errorf("timeline dates = %v; the flight departs Apr 1 (Sao Paulo), lands Apr 2 (Lisbon), hotel checks out Apr 5", dates)
	}
}

func TestDocumentUploadFlow(t *testing.T) {
	w := newWorld(t)
	body := `{"name":"Boarding pass","type":"BOARDING_PASS","fileName":"../../etc/pass.pdf","mimeType":"application/pdf","size":2048,"checksum":"` + checksum + `"}`

	res := w.post(t, "/documents", w.ana, body)
	doc, upload := res["document"].(map[string]any), res["upload"].(map[string]any)
	key := strings.TrimPrefix(upload["url"].(string), "https://storage.test/upload/")
	if doc["status"] != "PENDING" || doc["fileName"] != "pass.pdf" || doc["visibility"] != "TRIP" || upload["method"] != "PUT" || doc["ownerId"] != w.ana.ID {
		t.Fatalf("init = %v", res)
	}
	if _, leaked := doc["storageKey"]; leaked || strings.Contains(fmt.Sprint(doc), key) {
		t.Error("the storage key must never be exposed")
	}
	id := doc["id"].(string)

	w.problem(t, "POST", "/documents/"+id+"/complete", w.ana, "", 422, "upload_missing")
	if items := w.get(t, "/documents", w.bia)["items"].([]any); len(items) != 0 {
		t.Error("a pending document must be invisible to others")
	}
	w.problem(t, "GET", "/documents/"+id, w.bia, "", 404, "document_not_found")
	w.problem(t, "GET", "/documents/"+id+"/download", w.ana, "", 404, "document_not_found")

	w.Storage.Put(key, 999, checksum)
	w.problem(t, "POST", "/documents/"+id+"/complete", w.ana, "", 422, "upload_mismatch")
	if w.Storage.Has(key) {
		t.Error("a mismatching upload must be discarded")
	}

	w.Storage.Put(key, 2048, checksum)
	w.problem(t, "POST", "/documents/"+id+"/complete", w.caio, "", 404, "trip_not_found")
	done := apitest.Decode(t, w.Do("POST", w.base+"/documents/"+id+"/complete", w.ana.Token, ""))
	if done["status"] != "READY" || done["checksum"] != checksum || done["size"] != float64(2048) || done["mimeType"] != "application/pdf" {
		t.Errorf("complete = %v", done)
	}
	if again := w.Do("POST", w.base+"/documents/"+id+"/complete", w.ana.Token, ""); again.Code != 200 {
		t.Errorf("complete must be idempotent, got %d", again.Code)
	}

	if items := w.get(t, "/documents", w.bia)["items"].([]any); len(items) != 1 {
		t.Errorf("a ready document must be visible to the trip, got %d", len(items))
	}
	dl := w.get(t, "/documents/"+id+"/download", w.bia)["download"].(map[string]any)
	if dl["method"] != "GET" || !strings.Contains(dl["url"].(string), key) {
		t.Errorf("download = %v", dl)
	}
	if exp, _ := time.Parse(time.RFC3339, dl["expiresAt"].(string)); time.Until(exp) > 6*time.Minute {
		t.Errorf("a download link must be short-lived, expires in %v", time.Until(exp))
	}
	w.problem(t, "GET", "/documents/"+id+"/download", w.caio, "", 404, "trip_not_found")

	renamed := apitest.Decode(t, w.Do("PATCH", w.base+"/documents/"+id, w.ana.Token, `{"baseVersion":2,"name":"Renamed"}`))
	if renamed["name"] != "Renamed" || renamed["version"] != float64(3) || renamed["checksum"] != checksum {
		t.Errorf("a rename must bump the version and keep the checksum: %v", renamed)
	}
	w.problem(t, "PATCH", "/documents/"+id, w.bia, `{"baseVersion":3,"name":"x"}`, 403, "forbidden")

	if rec := w.Do("DELETE", w.base+"/documents/"+id, w.ana.Token, ""); rec.Code != 204 || !w.Storage.Has(key) == false && len(w.Storage.Deleted) == 0 {
		t.Errorf("delete = %d, storage deleted %v", rec.Code, w.Storage.Deleted)
	}
}

func TestDocumentRulesAndPrivacy(t *testing.T) {
	w := newWorld(t)
	valid := `"fileName":"a.pdf","mimeType":"application/pdf","size":10,"checksum":"` + checksum + `"`

	for name, body := range map[string]string{
		"bad mime":     `{"name":"x","type":"OTHER","fileName":"a.exe","mimeType":"application/x-msdownload","size":10,"checksum":"` + checksum + `"}`,
		"too large":    `{"name":"x","type":"OTHER","fileName":"a.pdf","mimeType":"application/pdf","size":99999999,"checksum":"` + checksum + `"}`,
		"bad checksum": `{"name":"x","type":"OTHER","fileName":"a.pdf","mimeType":"application/pdf","size":10,"checksum":"abc"}`,
		"bad type":     `{"name":"x","type":"MEME",` + valid + `}`,
		"bad link":     `{"name":"x","type":"OTHER","link":{"type":"spaceship","id":"nope"},` + valid + `}`,
	} {
		if b := w.problem(t, "POST", "/documents", w.ana, body, 422, "validation_failed"); len(apitest.Fields(b)) == 0 {
			t.Errorf("%s: no field errors", name)
		}
	}
	w.problem(t, "POST", "/documents", w.bia, `{"name":"x","type":"OTHER",`+valid+`}`, 403, "forbidden")

	res := w.post(t, "/documents", w.ana, `{"name":"Passport","type":"PASSPORT",`+valid+`}`)
	doc := res["document"].(map[string]any)
	if doc["visibility"] != "PRIVATE" {
		t.Errorf("a passport must default to PRIVATE, got %v", doc["visibility"])
	}
	key := strings.TrimPrefix(res["upload"].(map[string]any)["url"].(string), "https://storage.test/upload/")
	w.Storage.Put(key, 10, checksum)
	w.Do("POST", w.base+"/documents/"+doc["id"].(string)+"/complete", w.ana.Token, "")

	for _, who := range []apitest.Account{w.bia} {
		if items := w.get(t, "/documents", who)["items"].([]any); len(items) != 0 {
			t.Error("a private document must be invisible to other members")
		}
		w.problem(t, "GET", "/documents/"+doc["id"].(string), who, "", 404, "document_not_found")
		w.problem(t, "GET", "/documents/"+doc["id"].(string)+"/download", who, "", 404, "document_not_found")
	}
	if items := w.get(t, "/documents", w.ana)["items"].([]any); len(items) != 1 {
		t.Error("the owner must see their private document")
	}
}

func syncPath(w world) string { return w.base + "/sync" }

func (w world) push(t *testing.T, who apitest.Account, mutations string) []any {
	t.Helper()
	rec := w.Do("POST", syncPath(w), who.Token, `{"mutations":[`+mutations+`]}`)
	if rec.Code != 200 {
		t.Fatalf("push = %d %s", rec.Code, rec.Body)
	}
	return apitest.Decode(t, rec)["results"].([]any)
}

func (w world) pull(t *testing.T, who apitest.Account, cursor string) map[string]any {
	t.Helper()
	path := syncPath(w)
	if cursor != "" {
		path += "?cursor=" + cursor
	}
	rec := w.Do("GET", path, who.Token, "")
	if rec.Code != 200 {
		t.Fatalf("pull = %d %s", rec.Code, rec.Body)
	}
	return apitest.Decode(t, rec)
}

func changeIDs(pull map[string]any) map[string]string {
	out := map[string]string{}
	for _, c := range pull["changes"].([]any) {
		m := c.(map[string]any)
		out[m["id"].(string)] = m["entity"].(string) + ":" + m["op"].(string)
	}
	return out
}

func TestSyncPushAndPull(t *testing.T) {
	w := newWorld(t)
	dayID, itemID := "01a0bc21-0000-7000-8000-000000000001", "01a0bc21-0000-7000-8000-000000000002"
	m1, m2, m3, m4 := "01a0bc21-1111-7000-8000-000000000001", "01a0bc21-1111-7000-8000-000000000002", "01a0bc21-1111-7000-8000-000000000003", "01a0bc21-1111-7000-8000-000000000004"

	initial := w.pull(t, w.ana, "")
	if initial["hasMore"] != false || initial["resetRequired"] != false || initial["cursor"] == "" || initial["serverTime"] == "" {
		t.Errorf("initial pull = %v", initial)
	}
	if ids := changeIDs(initial); ids[w.trip] != "trip:upsert" {
		t.Errorf("the trip record must be part of the initial sync: %v", ids)
	}

	results := w.push(t, w.ana, fmt.Sprintf(
		`{"mutationId":%q,"entity":"itinerary_day","entityId":%q,"operation":"CREATE","payload":{"date":"2027-04-03","title":"Asakusa"}},
		 {"mutationId":%q,"entity":"itinerary_item","entityId":%q,"operation":"CREATE","payload":{"dayId":%q,"title":"Temple","category":"ATTRACTION"}}`, m1, dayID, m2, itemID, dayID))
	for i, r := range results {
		if r.(map[string]any)["status"] != "applied" || r.(map[string]any)["version"] != float64(1) {
			t.Errorf("result %d = %v", i, r)
		}
	}
	if rec := w.get(t, "/itinerary-items/"+itemID, w.ana); rec["title"] != "Temple" || rec["id"] != itemID {
		t.Errorf("an offline-created item must keep the client id: %v", rec)
	}

	retry := w.push(t, w.ana, fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_day","entityId":%q,"operation":"CREATE","payload":{"date":"2027-04-03","title":"Asakusa"}}`, m1, dayID))
	if retry[0].(map[string]any)["status"] != "duplicate" {
		t.Errorf("a retried mutation must be answered as a duplicate: %v", retry[0])
	}
	if days := w.get(t, "/itinerary-days", w.ana)["items"].([]any); len(days) != 1 {
		t.Errorf("the retry created %d days", len(days))
	}
	reused := w.push(t, w.ana, fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_day","entityId":%q,"operation":"CREATE","payload":{"date":"2027-04-04"}}`, m1, dayID))
	if reused[0].(map[string]any)["code"] != "mutation_id_reused" {
		t.Errorf("reusing a mutation id with other content: %v", reused[0])
	}

	updated := w.push(t, w.ana, fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_item","entityId":%q,"operation":"UPDATE","baseVersion":1,"payload":{"title":"Senso-ji"}}`, m3, itemID))[0].(map[string]any)
	if updated["status"] != "applied" || updated["version"] != float64(2) || updated["record"].(map[string]any)["title"] != "Senso-ji" {
		t.Errorf("update = %v", updated)
	}
	stale := w.push(t, w.ana, fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_item","entityId":%q,"operation":"UPDATE","baseVersion":1,"payload":{"title":"Stale"}}`, m4, itemID))[0].(map[string]any)
	if stale["status"] != "conflict" || stale["code"] != "version_conflict" || stale["version"] != float64(2) || stale["record"].(map[string]any)["title"] != "Senso-ji" {
		t.Errorf("a stale update must be a conflict carrying the server copy: %v", stale)
	}
	if got := w.get(t, "/itinerary-items/"+itemID, w.ana); got["title"] != "Senso-ji" {
		t.Errorf("a conflicting mutation overwrote the server: %v", got)
	}

	cursor := w.pull(t, w.ana, "")["cursor"].(string)
	m5, m6, m7 := "01a0bc21-1111-7000-8000-000000000005", "01a0bc21-1111-7000-8000-000000000006", "01a0bc21-1111-7000-8000-000000000007"
	del := w.push(t, w.ana, fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_item","entityId":%q,"operation":"DELETE","baseVersion":2}`, m5, itemID))[0].(map[string]any)
	again := w.push(t, w.ana, fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_item","entityId":%q,"operation":"DELETE"}`, m6, itemID))[0].(map[string]any)
	if del["status"] != "applied" || again["status"] != "applied" {
		t.Errorf("deleting must be idempotent: %v / %v", del, again)
	}
	gone := w.push(t, w.ana, fmt.Sprintf(`{"mutationId":%q,"entity":"itinerary_item","entityId":%q,"operation":"UPDATE","baseVersion":2,"payload":{"title":"Zombie"}}`, m7, itemID))[0].(map[string]any)
	if gone["status"] != "conflict" || gone["code"] != "entity_deleted" {
		t.Errorf("updating a deleted item: %v", gone)
	}

	incremental := w.pull(t, w.bia, cursor)
	if ids := changeIDs(incremental); ids[itemID] != "itinerary_item:delete" {
		t.Errorf("an incremental pull must carry the tombstone, got %v", ids)
	}
}

func TestSyncEnforcesPermissionsAndScope(t *testing.T) {
	w := newWorld(t)
	dayID := w.day(t, "2027-04-03")
	restaurant := w.post(t, "/restaurants", w.ana, `{"name":"Sushi Dai","reservationCode":"SECRET1"}`)

	w.problem(t, "GET", "/sync", w.caio, "", 404, "trip_not_found")
	w.problem(t, "POST", "/sync", w.caio, `{"mutations":[]}`, 404, "trip_not_found")
	w.problem(t, "GET", "/sync?cursor=garbage", w.ana, "", 400, "invalid_cursor")
	w.problem(t, "GET", "/sync?limit=0", w.ana, "", 400, "invalid_limit")

	viewerPull := w.pull(t, w.bia, "")
	for _, c := range viewerPull["changes"].([]any) {
		if rec, ok := c.(map[string]any)["record"].(map[string]any); ok && rec["name"] == "Sushi Dai" {
			if _, leaked := rec["reservationCode"]; leaked {
				t.Error("sync must apply the same redaction as the REST API")
			}
		}
	}
	if ids := changeIDs(w.pull(t, w.ana, "")); ids[restaurant["id"].(string)] != "restaurant:upsert" {
		t.Errorf("the writer must receive the restaurant: %v", ids)
	}

	r := w.push(t, w.bia, fmt.Sprintf(`{"mutationId":"01a0bc21-2222-7000-8000-000000000001","entity":"itinerary_item","entityId":"01a0bc21-2222-7000-8000-000000000002","operation":"CREATE","payload":{"dayId":%q,"title":"x","category":"OTHER"}}`, dayID))[0].(map[string]any)
	if r["status"] != "rejected" || r["code"] != "forbidden" {
		t.Errorf("a viewer's mutation must be rejected per mutation: %v", r)
	}

	other := w.CreateTrip(t, w.caio)
	otherPull := apitest.Decode(t, w.Do("GET", "/api/v1/trips/"+other+"/sync", w.caio.Token, ""))
	for id := range changeIDs(otherPull) {
		if id == restaurant["id"].(string) || id == dayID {
			t.Errorf("a pull leaked %s from another trip", id)
		}
	}
}

func TestSyncTripAndDocumentRules(t *testing.T) {
	w := newWorld(t)
	trip := apitest.Decode(t, w.Do("GET", "/api/v1/trips/"+w.trip, w.ana.Token, ""))

	r := w.push(t, w.ana, fmt.Sprintf(`{"mutationId":"01a0bc21-3333-7000-8000-000000000001","entity":"trip","entityId":%q,"operation":"UPDATE","baseVersion":%v,"payload":{"name":"Renamed offline"}}`, w.trip, trip["version"]))[0].(map[string]any)
	if r["status"] != "applied" || r["record"].(map[string]any)["name"] != "Renamed offline" {
		t.Errorf("trip update = %v", r)
	}
	forbidden := w.push(t, w.bia, fmt.Sprintf(`{"mutationId":"01a0bc21-3333-7000-8000-000000000002","entity":"trip","entityId":%q,"operation":"UPDATE","baseVersion":2,"payload":{"name":"Hijack"}}`, w.trip))[0].(map[string]any)
	if forbidden["status"] != "rejected" || forbidden["code"] != "forbidden" {
		t.Errorf("a viewer must not rename the trip through sync: %v", forbidden)
	}
	noCreate := w.push(t, w.ana, `{"mutationId":"01a0bc21-3333-7000-8000-000000000003","entity":"document","entityId":"01a0bc21-3333-7000-8000-000000000004","operation":"CREATE","payload":{}}`)[0].(map[string]any)
	if noCreate["code"] != "unsupported_operation" {
		t.Errorf("documents are created through the upload flow: %v", noCreate)
	}

	pending := w.post(t, "/documents", w.ana, `{"name":"P","type":"OTHER","fileName":"a.pdf","mimeType":"application/pdf","size":10,"checksum":"`+checksum+`"}`)["document"].(map[string]any)
	ready := w.post(t, "/documents", w.ana, `{"name":"R","type":"OTHER","fileName":"b.pdf","mimeType":"application/pdf","size":10,"checksum":"`+checksum+`"}`)
	readyDoc := ready["document"].(map[string]any)
	w.Storage.Put(strings.TrimPrefix(ready["upload"].(map[string]any)["url"].(string), "https://storage.test/upload/"), 10, checksum)
	w.Do("POST", w.base+"/documents/"+readyDoc["id"].(string)+"/complete", w.ana.Token, "")

	ids := changeIDs(w.pull(t, w.bia, ""))
	if _, seen := ids[pending["id"].(string)]; seen || ids[readyDoc["id"].(string)] != "document:upsert" {
		t.Errorf("only confirmed documents sync: %v", ids)
	}

	cursor := w.pull(t, w.bia, "")["cursor"].(string)
	w.Do("PATCH", w.base+"/documents/"+readyDoc["id"].(string), w.ana.Token, `{"baseVersion":2,"visibility":"PRIVATE"}`)
	if ids := changeIDs(w.pull(t, w.bia, cursor)); ids[readyDoc["id"].(string)] != "document:delete" {
		t.Errorf("a document turned private must disappear from other devices: %v", ids)
	}
}

func TestPlaceSearch(t *testing.T) {
	w := newWorld(t)
	w.Places.Results = []place.Candidate{{ProviderID: "ChIJ1", Name: "Senso-ji", Address: "Asakusa", Coordinates: &kernel.Coordinates{Lat: 35.7, Lng: 139.8}, Types: []string{"temple"}}}

	rec := w.Do("GET", "/api/v1/places/search?q=senso&lat=35.7&lng=139.7", w.ana.Token, "")
	items := apitest.Decode(t, rec)["items"].([]any)
	if rec.Code != 200 || len(items) != 1 || items[0].(map[string]any)["providerId"] != "ChIJ1" || items[0].(map[string]any)["latitude"] != 35.7 {
		t.Errorf("search = %d %s", rec.Code, rec.Body)
	}
	apitest.RequireProblem(t, w.Do("GET", "/api/v1/places/search?q=s", w.ana.Token, ""), 422, "validation_failed")
	apitest.RequireProblem(t, w.Do("GET", "/api/v1/places/search?q=senso&lat=abc", w.ana.Token, ""), 400, "invalid_lat")
	apitest.RequireProblem(t, w.Do("GET", "/api/v1/places/search?q=senso", "", ""), 401, "unauthenticated")
	w.Places.Err = fmt.Errorf("upstream exploded: secret-detail")
	body := apitest.RequireProblem(t, w.Do("GET", "/api/v1/places/search?q=senso", w.ana.Token, ""), 503, "provider_unavailable")
	if strings.Contains(fmt.Sprint(body), "secret-detail") {
		t.Error("provider errors must not leak")
	}
}

func TestGoogleCallsStopAtTheMonthlyLimit(t *testing.T) {
	w := newWorld(t)
	w.Places.Results = []place.Candidate{{ProviderID: "ChIJ1", Name: "Senso-ji"}}
	w.Routes.Routes = []routing.Route{{ExternalID: "r1", Duration: time.Minute}}
	plan := `{"stops":[{"location":{"name":"A"}},{"location":{"name":"B"}}]}`

	// Every allowed call is counted, one bucket per API.
	if rec := w.Do("GET", "/api/v1/places/search?q=senso", w.ana.Token, ""); rec.Code != 200 {
		t.Fatalf("search = %d %s", rec.Code, rec.Body)
	}
	w.Do("POST", w.base+"/maps/day", w.ana.Token, plan)
	if w.Quota.Used(google.BucketPlaces) != 1 || w.Quota.Used(google.BucketRoutes) != 1 {
		t.Errorf("used places=%d routes=%d, want 1 and 1", w.Quota.Used(google.BucketPlaces), w.Quota.Used(google.BucketRoutes))
	}

	// Once the places allowance is spent Google is never called again, but routes still work.
	w.Quota.Set(google.BucketPlaces, full.GoogleLimit)
	calls := len(w.Places.Queries)
	body := apitest.RequireProblem(t, w.Do("GET", "/api/v1/places/search?q=senso", w.ana.Token, ""), 503, "provider_quota_exhausted")
	if len(w.Places.Queries) != calls {
		t.Error("Google was called after the monthly limit was reached")
	}
	if strings.Contains(fmt.Sprint(body), "google_places") {
		t.Errorf("internal bucket names must not leak: %v", body)
	}
	if rec := w.Do("POST", w.base+"/maps/day", w.ana.Token, plan); rec.Code != 200 {
		t.Errorf("routes must keep working while only places are exhausted: %d %s", rec.Code, rec.Body)
	}

	w.Quota.Set(google.BucketRoutes, full.GoogleLimit)
	res := apitest.Decode(t, w.Do("POST", w.base+"/maps/day", w.ana.Token, plan))
	for _, leg := range res["legs"].([]any) {
		if leg.(map[string]any)["available"] == true {
			t.Errorf("leg = %v, want unavailable once the routes allowance is spent", leg)
		}
	}
}

func TestMapOfOnePlace(t *testing.T) {
	w := newWorld(t)
	png := []byte("\x89PNG pin")
	w.Maps.Image = kernel.MapImage{Data: png, ContentType: "image/png"}
	body := `{"location":{"name":"Torre de Tóquio","address":"Minato, Tokyo","latitude":35.6586,"longitude":139.7454},"language":"pt-BR"}`

	rec := w.Do("POST", w.base+"/maps/location", w.ana.Token, body)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Body.String() != string(png) {
		t.Fatalf("map = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if cache := rec.Header().Get("Cache-Control"); !strings.HasPrefix(cache, "private") {
		t.Errorf("Cache-Control = %q, want a private picture", cache)
	}
	pin := w.Maps.Pins[len(w.Maps.Pins)-1]
	if pin.Location.Name != "Torre de Tóquio" || pin.Location.Coordinates == nil || pin.Language != "pt-BR" {
		t.Errorf("pin = %+v", pin)
	}

	// Anyone who can read the trip sees its maps; strangers learn nothing.
	if rec := w.Do("POST", w.base+"/maps/location", w.bia.Token, body); rec.Code != 200 {
		t.Errorf("a viewer must see the map: %d", rec.Code)
	}
	w.problem(t, "POST", "/maps/location", w.caio, body, 404, "trip_not_found")
	apitest.RequireProblem(t, w.Do("POST", w.base+"/maps/location", "", body), 401, "unauthenticated")
	w.problem(t, "POST", "/maps/location", w.ana, `{"location":{}}`, 422, "validation_failed")

	w.Maps.Err = fmt.Errorf("upstream exploded: secret-detail")
	problem := w.problem(t, "POST", "/maps/location", w.ana, body, 503, "provider_unavailable")
	if strings.Contains(fmt.Sprint(problem), "secret-detail") {
		t.Error("provider errors must not leak")
	}
	w.Maps.Err = nil

	// Location maps share the picture allowance with route maps.
	w.Quota.Set(google.BucketMaps, full.GoogleLimit)
	drawn := len(w.Maps.Pins)
	w.problem(t, "POST", "/maps/location", w.ana, body, 503, "provider_quota_exhausted")
	if len(w.Maps.Pins) != drawn {
		t.Error("the renderer was called after the monthly limit was reached")
	}
}

func TestPlanTheDayOnAMap(t *testing.T) {
	w := newWorld(t)
	w.Routes.Routes = []routing.Route{{ExternalID: "r1", Duration: 12 * time.Minute, DistanceMeters: 2300, Polyline: "abc123"}}
	w.Maps.Image = kernel.MapImage{Data: []byte("\x89PNG day"), ContentType: "image/png"}
	stops := `[{"label":"Metropolitan Museum","location":{"name":"Met","latitude":40.7794,"longitude":-73.9632}},
		{"label":"Almoço","location":{"name":"Katz's","address":"205 E Houston St"}},
		{"label":"Jantar no mesmo lugar","location":{"name":"Katz's","address":"205 E Houston St"}},
		{"label":"Top of the Rock","location":{"name":"Top of the Rock","latitude":40.7593,"longitude":-73.9794}}]`
	body := `{"stops":` + stops + `,"mode":"WALKING","language":"pt-BR"}`

	rec := w.Do("POST", w.base+"/maps/day", w.ana.Token, body)
	res := apitest.Decode(t, rec)
	legs := res["legs"].([]any)
	if rec.Code != 200 || len(legs) != 3 {
		t.Fatalf("plan = %d %s", rec.Code, rec.Body)
	}
	first := legs[0].(map[string]any)
	if first["available"] != true || first["durationSeconds"] != float64(720) || first["distanceMeters"] != float64(2300) || first["polyline"] != "abc123" || first["from"] != float64(0) || first["to"] != float64(1) {
		t.Errorf("first leg = %v", first)
	}
	// Two stops at the same place need no trip, so the provider is asked only twice.
	same := legs[1].(map[string]any)
	if same["available"] != true || same["durationSeconds"] != nil || w.Routes.Calls.Load() != 2 {
		t.Errorf("same-place leg = %v after %d route calls, want a free leg and 2 calls", same, w.Routes.Calls.Load())
	}
	if res["image"] != nil {
		t.Error("no picture was asked for")
	}

	// The picture is optional, numbered, and made from the legs already found.
	withImage := apitest.Decode(t, w.Do("POST", w.base+"/maps/day", w.ana.Token, `{"stops":`+stops+`,"includeImage":true}`))
	if image, _ := withImage["image"].(string); !strings.HasPrefix(image, "data:image/png;base64,") {
		t.Errorf("image = %v", withImage["image"])
	}
	spec := w.Maps.Days[len(w.Maps.Days)-1]
	if len(spec.Stops) != 4 || spec.Stops[0].Label != "1" || spec.Stops[3].Label != "4" || len(spec.Paths) != 2 {
		t.Errorf("day spec = %+v, want 4 numbered stops and the 2 lines that exist", spec)
	}

	// Anyone who can read the trip may plan its map; strangers learn nothing.
	if rec := w.Do("POST", w.base+"/maps/day", w.bia.Token, body); rec.Code != 200 {
		t.Errorf("a viewer must see the day map: %d", rec.Code)
	}
	w.problem(t, "POST", "/maps/day", w.caio, body, 404, "trip_not_found")
	apitest.RequireProblem(t, w.Do("POST", w.base+"/maps/day", "", body), 401, "unauthenticated")
	w.problem(t, "POST", "/maps/day", w.ana, `{"stops":[{"location":{"name":"A"}}]}`, 422, "validation_failed")
	w.problem(t, "POST", "/maps/day", w.ana, `{"stops":[{"location":{"name":"A"}},{"location":{}}]}`, 422, "validation_failed")
	w.problem(t, "POST", "/maps/day", w.ana, `{"stops":[{"location":{"name":"A"}},{"location":{"name":"B"}}],"mode":"JETPACK"}`, 422, "validation_failed")
}

func TestPlanAWholeTripOnAMap(t *testing.T) {
	w := newWorld(t)
	w.Routes.Routes = []routing.Route{{ExternalID: "r1", Duration: 10 * time.Minute, DistanceMeters: 1500, Polyline: "abc123"}}
	w.Maps.Image = kernel.MapImage{Data: []byte("img"), ContentType: "image/png"}

	// Sixty places over a fortnight, each day its own group with its day number on the pin.
	var stops []string
	for i := 0; i < 60; i++ {
		day := i/4 + 1
		stops = append(stops, fmt.Sprintf(`{"label":"Parada %d","group":%d,"pin":"%d","location":{"name":"Lugar %d","latitude":%f,"longitude":%f}}`, i, day-1, day, i, 35.0+float64(i)*0.01, 139.0+float64(i)*0.01))
	}
	body := `{"stops":[` + strings.Join(stops, ",") + `],"includeImage":true}`

	res := apitest.Decode(t, w.Do("POST", w.base+"/maps/day", w.ana.Token, body))
	if legs := res["legs"].([]any); len(legs) != 59 {
		t.Fatalf("legs = %d, want one between each of the 60 places", len(legs))
	}
	if calls := w.Routes.Calls.Load(); calls != 59 {
		t.Errorf("route calls = %d, want 59", calls)
	}
	spec := w.Maps.Days[len(w.Maps.Days)-1]
	if len(spec.Stops) != 60 || spec.Stops[0].Label != "1" || spec.Stops[4].Label != "2" || spec.Stops[4].Group != 1 {
		t.Errorf("stops = %+v, want the day number on each pin and one group per day", spec.Stops[:6])
	}
	// The trip from the last place of day 1 to the first of day 2 belongs to day 2.
	if got := spec.Paths[3].Group; got != 1 {
		t.Errorf("crossing-days path group = %d, want the day it arrives in (1)", got)
	}

	// Past a trip's worth of places the request is refused, not silently cut.
	var tooMany []string
	for i := 0; i < 81; i++ {
		tooMany = append(tooMany, `{"location":{"name":"P"}}`)
	}
	w.problem(t, "POST", "/maps/day", w.ana, `{"stops":[`+strings.Join(tooMany, ",")+`]}`, 422, "validation_failed")
	w.problem(t, "POST", "/maps/day", w.ana, `{"stops":[{"group":-1,"location":{"name":"A"}},{"location":{"name":"B"}}]}`, 422, "validation_failed")
}

func TestPlanTheDayDegradesInsteadOfFailing(t *testing.T) {
	w := newWorld(t)
	w.Maps.Image = kernel.MapImage{Data: []byte("img"), ContentType: "image/png"}
	body := `{"stops":[{"location":{"name":"A"}},{"location":{"name":"B"}},{"location":{"name":"C"}}],"includeImage":true}`

	// The provider is down: the day still comes back, with no times invented.
	w.Routes.Err = fmt.Errorf("routes down: secret-detail")
	rec := w.Do("POST", w.base+"/maps/day", w.ana.Token, body)
	res := apitest.Decode(t, rec)
	for _, leg := range res["legs"].([]any) {
		if leg.(map[string]any)["available"] == true {
			t.Errorf("a leg with no route must not be marked available: %v", leg)
		}
	}
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "secret-detail") {
		t.Errorf("plan = %d %s", rec.Code, rec.Body)
	}
	w.Routes.Err = nil

	// A picture that cannot be drawn is left out; the legs are still useful.
	w.Maps.Err = fmt.Errorf("static maps down")
	res = apitest.Decode(t, w.Do("POST", w.base+"/maps/day", w.ana.Token, body))
	if res["image"] != nil || len(res["legs"].([]any)) != 2 {
		t.Errorf("res = %v", res)
	}
	w.Maps.Err = nil

	// A spent monthly allowance stops the provider calls: legs go unavailable, nothing is billed.
	w.Quota.Set(google.BucketRoutes, full.GoogleLimit)
	before := w.Routes.Calls.Load()
	res = apitest.Decode(t, w.Do("POST", w.base+"/maps/day", w.ana.Token, body))
	if w.Routes.Calls.Load() != before {
		t.Error("the route provider was called after the monthly limit was reached")
	}
	for _, leg := range res["legs"].([]any) {
		if leg.(map[string]any)["available"] == true {
			t.Errorf("leg = %v, want unavailable", leg)
		}
	}
}

// readyDocument uploads and confirms a document the way a client does, and returns its id.
func (w world) readyDocument(t *testing.T, who apitest.Account, body string) string {
	t.Helper()
	res := w.post(t, "/documents", who, body)
	doc, upload := res["document"].(map[string]any), res["upload"].(map[string]any)
	key := strings.TrimPrefix(upload["url"].(string), "https://storage.test/upload/")
	w.Storage.Put(key, 2048, checksum)
	id := doc["id"].(string)
	if rec := w.Do("POST", w.base+"/documents/"+id+"/complete", who.Token, ""); rec.Code != 200 {
		t.Fatalf("complete = %d %s", rec.Code, rec.Body)
	}
	return id
}

func TestTicketsLinkADocumentAndJoinTheTimeline(t *testing.T) {
	w := newWorld(t)
	docBody := func(vis string) string {
		return `{"name":"Ingresso Hamilton","type":"TICKET","fileName":"hamilton.pdf","mimeType":"application/pdf","size":2048,"checksum":"` + checksum + `","visibility":"` + vis + `"}`
	}
	doc := w.readyDocument(t, w.ana, docBody("TRIP"))

	ticket := w.post(t, "/tickets", w.ana, `{"name":"Hamilton","kind":"SHOW","location":{"name":"Richard Rodgers Theatre","address":"226 W 46th St"},"start":{"dateTime":"2027-04-03T19:00"},"end":{"dateTime":"2027-04-03T21:45"},"quantity":2,"seat":"Orquestra F 12-13","confirmationCode":"HAM123","documentId":"`+doc+`"}`)
	if ticket["kind"] != "SHOW" || ticket["quantity"] != float64(2) || ticket["documentId"] != doc || ticket["status"] != "PLANNED" ||
		ticket["start"].(map[string]any)["timezone"] != "Asia/Tokyo" || ticket["location"].(map[string]any)["name"] != "Richard Rodgers Theatre" {
		t.Fatalf("ticket = %v", ticket)
	}
	id := ticket["id"].(string)

	// A viewer sees the ticket and its file, but not the confirmation code.
	seen := w.get(t, "/tickets/"+id, w.bia)
	if _, leaked := seen["confirmationCode"]; leaked || seen["documentId"] != doc {
		t.Errorf("viewer sees %v", seen)
	}

	// A ticket with a time is a line of its day on the timeline.
	var found bool
	for _, d := range w.get(t, "/itinerary", w.ana)["days"].([]any) {
		day := d.(map[string]any)
		if day["date"] != "2027-04-03" {
			continue
		}
		for _, e := range day["entries"].([]any) {
			entry := e.(map[string]any)
			if entry["kind"] == "ticket" && entry["id"] == id && entry["title"] == "Hamilton" && entry["subtitle"] == "SHOW" {
				found = true
			}
		}
	}
	if !found {
		t.Error("the ticket is missing from its day on the timeline")
	}

	// Without a time it is not on the timeline, and the file can be taken off it.
	loose := w.post(t, "/tickets", w.ana, `{"name":"Museu"}`)
	if loose["quantity"] != float64(1) || loose["kind"] != "ATTRACTION" {
		t.Errorf("defaults = %v", loose)
	}
	cleared := apitest.Decode(t, w.Do("PATCH", w.base+"/tickets/"+id, w.ana.Token, `{"baseVersion":1,"documentId":null}`))
	if _, still := cleared["documentId"]; still {
		t.Errorf("documentId = %v after clearing", cleared["documentId"])
	}
}

func TestTicketFilesAndRules(t *testing.T) {
	w := newWorld(t)
	private := w.readyDocument(t, w.ana, `{"name":"Passaporte","type":"PASSPORT","fileName":"p.pdf","mimeType":"application/pdf","size":2048,"checksum":"`+checksum+`"}`)
	pending := w.post(t, "/documents", w.ana, `{"name":"Pendente","type":"TICKET","fileName":"x.pdf","mimeType":"application/pdf","size":2048,"checksum":"`+checksum+`"}`)["document"].(map[string]any)["id"].(string)

	for name, body := range map[string]string{
		"no name":           `{"kind":"SHOW"}`,
		"bad kind":          `{"name":"X","kind":"CIRCUS"}`,
		"end before start":  `{"name":"X","start":{"dateTime":"2027-04-03T20:00"},"end":{"dateTime":"2027-04-03T19:00"}}`,
		"end without start": `{"name":"X","end":{"dateTime":"2027-04-03T19:00"}}`,
		"zero tickets":      `{"name":"X","quantity":0}`,
		"not a uuid":        `{"name":"X","documentId":"abc"}`,
		"unknown document":  `{"name":"X","documentId":"01a0c09e-ba92-7abe-96c8-0b4d06f661f4"}`,
		"unfinished file":   `{"name":"X","documentId":"` + pending + `"}`,
	} {
		t.Run(name, func(t *testing.T) { w.problem(t, "POST", "/tickets", w.ana, body, 422, "validation_failed") })
	}

	// Somebody else's private document cannot be attached to a ticket.
	w.AddMember(t, w.trip, w.ana, w.caio, "MEMBER")
	w.problem(t, "POST", "/tickets", w.caio, `{"name":"X","documentId":"`+private+`"}`, 422, "validation_failed")

	// A viewer cannot create tickets.
	w.problem(t, "POST", "/tickets", w.bia, `{"name":"X"}`, 403, "forbidden")
}

func TestExpensesPlannedAndPaid(t *testing.T) {
	w := newWorld(t)
	store := "01a0c09e-ba92-7abe-96c8-0b4d06f661f4"

	// Something meant to be bought: it has an estimate, is tied to a shop and is not paid yet.
	want := w.post(t, "/expenses", w.ana, `{"name":"Switch OLED","category":"ELECTRONICS","estimate":{"amount":34999,"currency":"USD"},"link":{"type":"place","id":"`+store+`"}}`)
	if want["status"] != "PLANNED" || want["category"] != "ELECTRONICS" || want["link"].(map[string]any)["id"] != store || want["actual"] != nil {
		t.Fatalf("planned expense = %v", want)
	}
	id := want["id"].(string)

	// Buying it records what was really paid and keeps the estimate to compare against.
	bought := apitest.Decode(t, w.Do("PATCH", w.base+"/expenses/"+id, w.ana.Token, `{"baseVersion":1,"status":"PAID","actual":{"amount":32999,"currency":"USD"},"date":"2027-04-05"}`))
	if bought["status"] != "PAID" || bought["actual"].(map[string]any)["amount"] != float64(32999) || bought["estimate"].(map[string]any)["amount"] != float64(34999) || bought["date"] != "2027-04-05" {
		t.Errorf("paid expense = %v", bought)
	}

	// The link can be taken off and the expense is still there.
	cleared := apitest.Decode(t, w.Do("PATCH", w.base+"/expenses/"+id, w.ana.Token, `{"baseVersion":2,"link":null}`))
	if _, still := cleared["link"]; still {
		t.Errorf("link = %v after clearing", cleared["link"])
	}

	// Read by everyone in the trip, changed only by those who may.
	if items := w.get(t, "/expenses", w.bia)["items"].([]any); len(items) != 1 {
		t.Errorf("a viewer sees %d expenses, want 1", len(items))
	}
	w.problem(t, "POST", "/expenses", w.bia, `{"name":"x","estimate":{"amount":100,"currency":"USD"}}`, 403, "forbidden")

	for name, body := range map[string]string{
		"no name":           `{"estimate":{"amount":100,"currency":"USD"}}`,
		"no amount at all":  `{"name":"Almoço"}`,
		"paid without paid": `{"name":"Almoço","status":"PAID","estimate":{"amount":100,"currency":"USD"}}`,
		"planned but paid":  `{"name":"Almoço","status":"PLANNED","actual":{"amount":100,"currency":"USD"}}`,
		"negative":          `{"name":"Almoço","estimate":{"amount":-1,"currency":"USD"}}`,
		"bad category":      `{"name":"Almoço","category":"YACHT","estimate":{"amount":100,"currency":"USD"}}`,
		"bad date":          `{"name":"Almoço","estimate":{"amount":100,"currency":"USD"},"date":"tomorrow"}`,
		"bad link type":     `{"name":"Almoço","estimate":{"amount":100,"currency":"USD"},"link":{"type":"moon","id":"` + store + `"}}`,
		"bad link id":       `{"name":"Almoço","estimate":{"amount":100,"currency":"USD"},"link":{"type":"place","id":"nope"}}`,
		"mixed currencies":  `{"name":"Almoço","status":"PAID","estimate":{"amount":100,"currency":"USD"},"actual":{"amount":100,"currency":"BRL"}}`,
		"unknown currency":  `{"name":"Almoço","estimate":{"amount":100,"currency":"ZZZ"}}`,
	} {
		t.Run(name, func(t *testing.T) { w.problem(t, "POST", "/expenses", w.ana, body, 422, "validation_failed") })
	}
}

func TestBudgetHasOneLimitPerCategory(t *testing.T) {
	w := newWorld(t)
	total := w.post(t, "/budget-limits", w.ana, `{"category":"TOTAL","amount":{"amount":500000,"currency":"USD"}}`)
	w.post(t, "/budget-limits", w.ana, `{"category":"FOOD","amount":{"amount":120000,"currency":"USD"}}`)
	w.problem(t, "POST", "/budget-limits", w.ana, `{"category":"TOTAL","amount":{"amount":1,"currency":"USD"}}`, 409, "budget_exists")
	w.problem(t, "POST", "/budget-limits", w.ana, `{"category":"YACHT","amount":{"amount":1,"currency":"USD"}}`, 422, "validation_failed")
	w.problem(t, "POST", "/budget-limits", w.bia, `{"category":"CLOTHES","amount":{"amount":1,"currency":"USD"}}`, 403, "forbidden")

	raised := apitest.Decode(t, w.Do("PATCH", w.base+"/budget-limits/"+total["id"].(string), w.ana.Token, `{"baseVersion":1,"amount":{"amount":650000,"currency":"USD"}}`))
	if raised["amount"].(map[string]any)["amount"] != float64(650000) || raised["category"] != "TOTAL" {
		t.Errorf("raised = %v", raised)
	}
	if items := w.get(t, "/budget-limits", w.bia)["items"].([]any); len(items) != 2 {
		t.Errorf("a viewer sees %d limits, want 2", len(items))
	}
	// A limit that was removed can be set again.
	if rec := w.Do("DELETE", w.base+"/budget-limits/"+total["id"].(string), w.ana.Token, ""); rec.Code != 204 {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	w.post(t, "/budget-limits", w.ana, `{"category":"TOTAL","amount":{"amount":100,"currency":"USD"}}`)
}

func TestFlightsAndHotelsCarryTheirPrice(t *testing.T) {
	w := newWorld(t)
	flight := w.post(t, "/flights", w.ana, `{"flightNumber":"LA8180","departureAirport":"GRU","arrivalAirport":"JFK","departure":{"dateTime":"2027-04-01T22:50","timezone":"America/Sao_Paulo"},"arrival":{"dateTime":"2027-04-02T06:45","timezone":"America/New_York"},"cost":{"amount":420000,"currency":"BRL"}}`)
	if flight["cost"].(map[string]any)["amount"] != float64(420000) {
		t.Errorf("flight = %v", flight)
	}
	hotel := w.post(t, "/hotels", w.ana, `{"name":"Park Hyatt","checkIn":{"dateTime":"2027-04-02T15:00"},"checkOut":{"dateTime":"2027-04-05T11:00"},"cost":{"amount":180000,"currency":"USD"}}`)
	id := hotel["id"].(string)
	if hotel["cost"].(map[string]any)["currency"] != "USD" {
		t.Errorf("hotel = %v", hotel)
	}
	// Everyone in the trip sees the price; it can be changed and taken off.
	if w.get(t, "/hotels/"+id, w.bia)["cost"] == nil {
		t.Error("a viewer must see the price")
	}
	raised := apitest.Decode(t, w.Do("PATCH", w.base+"/hotels/"+id, w.ana.Token, `{"baseVersion":1,"cost":{"amount":200000,"currency":"USD"}}`))
	if raised["cost"].(map[string]any)["amount"] != float64(200000) {
		t.Errorf("raised = %v", raised)
	}
	cleared := apitest.Decode(t, w.Do("PATCH", w.base+"/hotels/"+id, w.ana.Token, `{"baseVersion":2,"cost":null}`))
	if _, still := cleared["cost"]; still {
		t.Errorf("cost = %v after clearing", cleared["cost"])
	}
	w.problem(t, "POST", "/hotels", w.ana, `{"name":"H","checkIn":{"dateTime":"2027-04-02T15:00"},"checkOut":{"dateTime":"2027-04-05T11:00"},"cost":{"amount":-5,"currency":"USD"}}`, 422, "validation_failed")
}

func TestPaymentMarksSayWhetherAPriceIsPaid(t *testing.T) {
	w := newWorld(t)
	ticket := "01a0c09e-ba92-7abe-96c8-0b4d06f661f4"

	mark := w.post(t, "/payments", w.ana, `{"link":{"type":"ticket","id":"`+ticket+`"}}`)
	if mark["paid"] != true || mark["link"].(map[string]any)["id"] != ticket {
		t.Fatalf("payment = %v", mark)
	}
	// One mark per priced record; changing it goes through the mark that exists.
	w.problem(t, "POST", "/payments", w.ana, `{"link":{"type":"ticket","id":"`+ticket+`"}}`, 409, "payment_exists")
	unpaid := apitest.Decode(t, w.Do("PATCH", w.base+"/payments/"+mark["id"].(string), w.ana.Token, `{"baseVersion":1,"paid":false}`))
	if unpaid["paid"] != false {
		t.Errorf("unpaid = %v", unpaid)
	}

	w.problem(t, "POST", "/payments", w.ana, `{"link":{"type":"moon","id":"`+ticket+`"}}`, 422, "validation_failed")
	w.problem(t, "POST", "/payments", w.ana, `{"link":{"type":"ticket","id":"nope"}}`, 422, "validation_failed")
	w.problem(t, "POST", "/payments", w.bia, `{"link":{"type":"hotel","id":"`+ticket+`"}}`, 403, "forbidden")
	if items := w.get(t, "/payments", w.bia)["items"].([]any); len(items) != 1 {
		t.Errorf("a viewer sees %d marks, want 1", len(items))
	}

	// Taking the mark off lets the price be marked again.
	if rec := w.Do("DELETE", w.base+"/payments/"+mark["id"].(string), w.ana.Token, ""); rec.Code != 204 {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	w.post(t, "/payments", w.ana, `{"link":{"type":"ticket","id":"`+ticket+`"},"paid":false}`)
}
