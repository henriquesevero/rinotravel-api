package google_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rinotravel-api/internal/google"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/routing"
)

func fakeGoogle(t *testing.T, status int, response string, seen *seenRequest) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			body, _ := io.ReadAll(r.Body)
			*seen = seenRequest{Method: r.Method, Path: r.URL.RequestURI(), Key: r.Header.Get("X-Goog-Api-Key"), Mask: r.Header.Get("X-Goog-FieldMask"), Body: string(body)}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server
}

type seenRequest struct{ Method, Path, Key, Mask, Body string }

func TestPlacesSearch(t *testing.T) {
	var seen seenRequest
	server := fakeGoogle(t, 200, `{"places":[
		{"id":"ChIJ1","displayName":{"text":"Senso-ji"},"formattedAddress":"Asakusa, Tokyo","location":{"latitude":35.7148,"longitude":139.7967},"types":["temple"]},
		{"id":"ChIJ2","displayName":{"text":"No coordinates"}}]}`, &seen)
	provider := google.NewPlacesWithBase("secret-key", server.URL, nil)

	found, err := provider.Search(context.Background(), "senso", &kernel.Coordinates{Lat: 35.7, Lng: 139.7}, "pt-BR")

	if err != nil || len(found) != 2 || found[0].ProviderID != "ChIJ1" || found[0].Name != "Senso-ji" || found[0].Coordinates.Lat != 35.7148 || found[1].Coordinates != nil {
		t.Fatalf("Search() = %+v, %v", found, err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(seen.Body), &body)
	if seen.Method != "POST" || seen.Path != "/v1/places:searchText" || seen.Key != "secret-key" || !strings.Contains(seen.Mask, "places.id") ||
		body["textQuery"] != "senso" || body["languageCode"] != "pt-BR" || body["locationBias"] == nil {
		t.Errorf("request = %+v body %v", seen, body)
	}
}

func TestPlacesDetailsAndErrors(t *testing.T) {
	var seen seenRequest
	ok := fakeGoogle(t, 200, `{"id":"ChIJ1","displayName":{"text":"Senso-ji"},"formattedAddress":"Asakusa"}`, &seen)
	c, err := google.NewPlacesWithBase("k", ok.URL, nil).Details(context.Background(), "ChIJ1", "en")
	if err != nil || c.Name != "Senso-ji" || !strings.HasPrefix(seen.Path, "/v1/places/ChIJ1?languageCode=en") {
		t.Errorf("Details() = %+v, %v (path %s)", c, err, seen.Path)
	}

	failing := fakeGoogle(t, 403, `{"error":{"status":"PERMISSION_DENIED","message":"API key not valid"}}`, nil)
	_, err = google.NewPlacesWithBase("super-secret-key", failing.URL, nil).Search(context.Background(), "x", nil, "en")
	if err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") || strings.Contains(err.Error(), "super-secret-key") {
		t.Errorf("error = %v; want the upstream status without the key", err)
	}

	garbled := fakeGoogle(t, 200, `not json`, nil)
	if _, err := google.NewPlacesWithBase("k", garbled.URL, nil).Search(context.Background(), "x", nil, "en"); err == nil {
		t.Error("an undecodable response must be an error")
	}
	if _, err := google.NewPlacesWithBase("k", "http://127.0.0.1:1", nil).Search(context.Background(), "x", nil, "en"); err == nil {
		t.Error("an unreachable provider must be an error")
	}
}

const routesResponse = `{"routes":[{"duration":"2700s","distanceMeters":21500,"legs":[{"steps":[
 {"travelMode":"WALK","staticDuration":"120s","startLocation":{"latLng":{"latitude":1,"longitude":1}},"endLocation":{"latLng":{"latitude":1.1,"longitude":1.1}},"navigationInstruction":{"instructions":"Head north"}},
 {"travelMode":"WALK","staticDuration":"180s","startLocation":{"latLng":{"latitude":1.1,"longitude":1.1}},"endLocation":{"latLng":{"latitude":1.2,"longitude":1.2}},"navigationInstruction":{"instructions":"Turn left"}},
 {"travelMode":"TRANSIT","staticDuration":"1200s","startLocation":{"latLng":{"latitude":1.2,"longitude":1.2}},"endLocation":{"latLng":{"latitude":2,"longitude":2}},
  "transitDetails":{"stopDetails":{"departureStop":{"name":"Station A"},"arrivalStop":{"name":"Station B"},"departureTime":"2027-04-02T01:00:00Z","arrivalTime":"2027-04-02T01:20:00Z"},
   "transitLine":{"name":"Line E","nameShort":"E","vehicle":{"type":"SUBWAY"}},"headsign":"Uptown","stopCount":7}},
 {"travelMode":"TRANSIT","staticDuration":"600s","startLocation":{"latLng":{"latitude":2,"longitude":2}},"endLocation":{"latLng":{"latitude":3,"longitude":3}},
  "transitDetails":{"stopDetails":{"departureStop":{"name":"B"},"arrivalStop":{"name":"C"}},"transitLine":{"name":"Airport Bus","vehicle":{"type":"BUS"}},"stopCount":2}},
 {"travelMode":"WALK","staticDuration":"60s","startLocation":{"latLng":{"latitude":3,"longitude":3}},"endLocation":{"latLng":{"latitude":3.1,"longitude":3.1}}}
]}]}]}`

func TestRoutesComputeMapsGoogleStepsToLegs(t *testing.T) {
	var seen seenRequest
	server := fakeGoogle(t, 200, routesResponse, &seen)
	when := time.Date(2027, 4, 2, 0, 30, 0, 0, time.UTC)

	routes, err := google.NewRoutesWithBase("k", server.URL, nil).Compute(context.Background(), routing.RouteRequest{
		Origin:      kernel.Location{Name: "Airport", Coordinates: &kernel.Coordinates{Lat: 1, Lng: 1}},
		Destination: kernel.Location{Address: "Shinjuku, Tokyo"},
		DepartureAt: &when, Language: "pt-BR",
	})

	if err != nil || len(routes) != 1 {
		t.Fatalf("Compute() = %+v, %v", routes, err)
	}
	route := routes[0]
	if route.Duration != 45*time.Minute || route.DistanceMeters != 21500 || len(route.Legs) != 4 {
		t.Fatalf("route = %+v; want the two consecutive walks merged into one leg (4 legs)", route)
	}
	walk, subway, bus, last := route.Legs[0], route.Legs[1], route.Legs[2], route.Legs[3]
	if walk.Mode != routing.ModeWalking || walk.Duration != 5*time.Minute || walk.Instructions != "Head north Turn left" {
		t.Errorf("merged walk = %+v", walk)
	}
	if subway.Mode != routing.ModeSubway || subway.Line != "E" || subway.Direction != "Uptown" || *subway.Stops != 7 || subway.Origin.Name != "Station A" ||
		subway.Departure == nil || !subway.Departure.Equal(time.Date(2027, 4, 2, 1, 0, 0, 0, time.UTC)) || subway.Arrival == nil {
		t.Errorf("subway = %+v", subway)
	}
	if bus.Mode != routing.ModeBus || bus.Line != "Airport Bus" || last.Mode != routing.ModeWalking {
		t.Errorf("bus/last = %+v / %+v", bus, last)
	}

	var body map[string]any
	_ = json.Unmarshal([]byte(seen.Body), &body)
	origin := body["origin"].(map[string]any)["location"].(map[string]any)["latLng"].(map[string]any)
	if seen.Path != "/directions/v2:computeRoutes" || body["travelMode"] != "TRANSIT" || body["languageCode"] != "pt-BR" || origin["latitude"] != float64(1) ||
		body["destination"].(map[string]any)["address"] != "Shinjuku, Tokyo" || body["departureTime"] != "2027-04-02T00:30:00Z" {
		t.Errorf("request body = %v", body)
	}
}

func TestRoutesTravelModeMapping(t *testing.T) {
	cases := map[routing.Mode]string{routing.ModeWalking: "WALK", routing.ModeCar: "DRIVE", routing.ModeTaxi: "DRIVE", routing.ModeRideshare: "DRIVE", routing.ModeSubway: "TRANSIT", "": "TRANSIT"}
	for mode, want := range cases {
		var seen seenRequest
		server := fakeGoogle(t, 200, `{"routes":[]}`, &seen)
		if _, err := google.NewRoutesWithBase("k", server.URL, nil).Compute(context.Background(), routing.RouteRequest{Origin: kernel.Location{Name: "A"}, Destination: kernel.Location{Name: "B"}, Mode: mode}); err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.Unmarshal([]byte(seen.Body), &body)
		if body["travelMode"] != want {
			t.Errorf("mode %q -> %v, want %s", mode, body["travelMode"], want)
		}
	}
}

func TestRoutesUpstreamFailure(t *testing.T) {
	server := fakeGoogle(t, 500, `{"error":{"status":"INTERNAL","message":"oops"}}`, nil)

	_, err := google.NewRoutesWithBase("k", server.URL, nil).Compute(context.Background(), routing.RouteRequest{})

	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v", err)
	}
}
