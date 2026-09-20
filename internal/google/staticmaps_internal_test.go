package google

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/transfer"
)

func spec(polyline string) transfer.MapSpec {
	return transfer.MapSpec{
		Origin:      kernel.Location{Name: "JFK", Coordinates: &kernel.Coordinates{Lat: 40.6413, Lng: -73.7781}},
		Destination: kernel.Location{Name: "Times Square", Address: "Times Sq, New York"},
		Polyline:    polyline,
		Language:    "pt-BR",
	}
}

func TestStaticMapURL_DrawsBothEndsAndTheRoute(t *testing.T) {
	raw := staticMapURL("https://maps.example", "the-key", spec(`_p~iF~ps|U_ulLnnqC_mqNvxq`+"`"+`@`))
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()

	if parsed.Path != "/maps/api/staticmap" || q.Get("key") != "the-key" || q.Get("language") != "pt-BR" || q.Get("size") != "640x360" {
		t.Errorf("url = %s", raw)
	}
	markers := q["markers"]
	if len(markers) != 2 || !strings.HasSuffix(markers[0], "|label:A|40.641300,-73.778100") || !strings.HasSuffix(markers[1], "|label:B|Times Sq, New York") {
		t.Errorf("markers = %v (coordinates win, then the address)", markers)
	}
	if path := q.Get("path"); !strings.Contains(path, "|enc:_p~iF~ps|U_ulLnnqC_mqNvxq`@") {
		t.Errorf("path = %q, want the encoded line kept intact", path)
	}
}

func TestStaticMapURL_DropsTheLineWhenTheURLWouldBeTooLong(t *testing.T) {
	raw := staticMapURL("https://maps.example", "k", spec(strings.Repeat("a", 9000)))

	if len(raw) > maxStaticURL {
		t.Errorf("url has %d characters, over the %d limit", len(raw), maxStaticURL)
	}
	q, _ := url.Parse(raw)
	if q.Query().Get("path") != "" || len(q.Query()["markers"]) != 2 {
		t.Errorf("a too-long line must fall back to the two markers: %s", raw)
	}
}

func TestStaticMapURL_WithoutALineStillHasMarkers(t *testing.T) {
	q, _ := url.Parse(staticMapURL("https://maps.example", "k", spec("")))
	if q.Query().Get("path") != "" || len(q.Query()["markers"]) != 2 {
		t.Errorf("query = %v", q.Query())
	}
}

func TestStaticMaps_Render(t *testing.T) {
	png := []byte("\x89PNG fake image")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	t.Cleanup(server.Close)

	image, err := NewStaticMapsWithBase("k", server.URL, nil).Render(context.Background(), spec(""))
	if err != nil || image.ContentType != "image/png" || string(image.Data) != string(png) {
		t.Fatalf("Render() = %+v, %v", image, err)
	}
}

func TestStaticMaps_ExplainsARefusalWithoutLeakingTheKey(t *testing.T) {
	const key = "AIza-very-secret-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("This API project is not authorized to use this API."))
	}))
	t.Cleanup(server.Close)

	_, err := NewStaticMapsWithBase(key, server.URL, nil).Render(context.Background(), spec(""))

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

	_, err := NewStaticMapsWithBase(key, base, nil).Render(context.Background(), spec(""))
	if err == nil || strings.Contains(err.Error(), key) {
		t.Errorf("err = %v, want a failure that does not contain the key", err)
	}
}
