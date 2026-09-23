package google

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/place"
)

func TestStaticPinURL_MarksOnePlaceAtStreetLevel(t *testing.T) {
	withPoint := staticPinURL("https://maps.example", "the-key", place.PinSpec{
		Location: kernel.Location{Name: "Torre", Address: "Minato, Tokyo", Coordinates: &kernel.Coordinates{Lat: 35.6586, Lng: 139.7454}},
		Language: "pt-BR",
	})
	q, _ := url.Parse(withPoint)
	if q.Query().Get("zoom") != "15" || q.Query().Get("key") != "the-key" || q.Query().Get("language") != "pt-BR" {
		t.Errorf("url = %s", withPoint)
	}
	if markers := q.Query()["markers"]; len(markers) != 1 || !strings.HasSuffix(markers[0], "|35.658600,139.745400") {
		t.Errorf("markers = %v, want the coordinates to win", markers)
	}

	byAddress, _ := url.Parse(staticPinURL("https://maps.example", "k", place.PinSpec{Location: kernel.Location{Address: "Times Sq, New York"}}))
	if markers := byAddress.Query()["markers"]; len(markers) != 1 || !strings.HasSuffix(markers[0], "|Times Sq, New York") {
		t.Errorf("markers = %v, want the address", markers)
	}
}

func TestStaticMaps_RenderPin(t *testing.T) {
	png := []byte("\x89PNG pin")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("zoom") == "" {
			http.Error(w, "missing zoom", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	t.Cleanup(server.Close)

	image, err := NewStaticMapsWithBase("k", server.URL, nil).RenderPin(context.Background(), place.PinSpec{Location: kernel.Location{Name: "Torre"}})
	if err != nil || string(image.Data) != string(png) {
		t.Fatalf("RenderPin() = %+v, %v", image, err)
	}
}

// The two tests below exercise fetch(), shared by RenderPin and RenderDay; RenderPin stands in for
// either.
func TestStaticMaps_ExplainsARefusalWithoutLeakingTheKey(t *testing.T) {
	const key = "AIza-very-secret-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("This API project is not authorized to use this API."))
	}))
	t.Cleanup(server.Close)

	_, err := NewStaticMapsWithBase(key, server.URL, nil).RenderPin(context.Background(), place.PinSpec{Location: kernel.Location{Name: "Torre"}})

	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err = %v, want the status and Google's own explanation", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("the error leaks the API key: %v", err)
	}
}

func TestStaticMaps_TransportErrorsDoNotLeakTheKey(t *testing.T) {
	const key = "AIza-very-secret-key"
	server := httptest.NewServer(http.NotFoundHandler())
	base := server.URL
	server.Close() // nothing is listening any more

	_, err := NewStaticMapsWithBase(key, base, nil).RenderPin(context.Background(), place.PinSpec{Location: kernel.Location{Name: "Torre"}})
	if err == nil || strings.Contains(err.Error(), key) {
		t.Errorf("err = %v, want a failure that does not contain the key", err)
	}
}
