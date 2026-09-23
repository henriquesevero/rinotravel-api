package google

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"rinotravel-api/internal/daymap"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/place"
)

const (
	defaultStaticMapsBase = "https://maps.googleapis.com"
	// maxStaticURL keeps the request under Google's limit (16,384 characters). A route line long enough to
	// pass it is dropped and the map falls back to the markers.
	maxStaticURL     = 14000
	staticMapSize    = "640x360"
	brandBlueMarker  = "0x2563EB"
	maxMapImageBytes = 2 << 20
)

// StaticMaps draws a route picture with the Maps Static API. The key travels in the URL because the
// API has no other way to take it, so URLs never reach a log or an error message.
type StaticMaps struct {
	key  string
	base string
	http *http.Client
}

func NewStaticMaps(apiKey string) *StaticMaps {
	return &StaticMaps{key: apiKey, base: defaultStaticMapsBase, http: &http.Client{Timeout: 10 * time.Second}}
}

// NewStaticMapsWithBase points the adapter at another server; tests use it with a fake Google.
func NewStaticMapsWithBase(apiKey, base string, httpClient *http.Client) *StaticMaps {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &StaticMaps{key: apiKey, base: base, http: httpClient}
}

func point(l kernel.Location) string {
	switch {
	case l.Coordinates != nil:
		return fmt.Sprintf("%.6f,%.6f", l.Coordinates.Lat, l.Coordinates.Lng)
	case l.Address != "":
		return l.Address
	default:
		return l.Name
	}
}

// dayTolerances are how coarse the route lines may get, in degrees (0.0001 is about 11 metres),
// tried in turn until the request fits in a URL.
var dayTolerances = []float64{0, 0.00005, 0.0002, 0.0008, 0.003}

// groupColors give each day of a trip its own colour, repeating after eight.
var groupColors = []string{"0x2563EB", "0xF97316", "0x16A34A", "0x7C3AED", "0xDB2777", "0x0D9488", "0xD97706", "0xDC2626"}

func groupColor(group int) string { return groupColors[group%len(groupColors)] }

// markerPoint is where the pin of stop i goes. A stop with coordinates is pinned there. One known only
// by a name is pinned where the route to it ends (or, for the first stop, where the route from it
// begins): the map service reads a bare name in the language of the request and can put it in another
// country, while the route was already found for the right place.
func markerPoint(spec daymap.DaySpec, i int) string {
	stop := spec.Stops[i].Location
	if stop.Coordinates != nil {
		return point(stop)
	}
	for _, path := range spec.Paths {
		if path.To != i || path.To <= path.From {
			continue
		}
		if points := decodePolyline(path.Encoded); len(points) > 0 {
			end := points[len(points)-1]
			return fmt.Sprintf("%.6f,%.6f", end.lat, end.lng)
		}
	}
	for _, path := range spec.Paths {
		if path.From != i || path.To <= path.From {
			continue
		}
		if points := decodePolyline(path.Encoded); len(points) > 0 {
			return fmt.Sprintf("%.6f,%.6f", points[0].lat, points[0].lng)
		}
	}
	return point(stop)
}

func staticDayURL(base, key string, spec daymap.DaySpec) string {
	build := func(paths []daymap.Path) string {
		q := url.Values{}
		q.Set("size", staticMapSize)
		q.Set("scale", "2")
		q.Set("maptype", "roadmap")
		q.Set("format", "png")
		if spec.Language != "" {
			q.Set("language", spec.Language)
		}
		for i, stop := range spec.Stops {
			marker := "color:" + groupColor(stop.Group)
			if stop.Label != "" {
				marker += "|label:" + stop.Label
			}
			q.Add("markers", marker+"|"+markerPoint(spec, i))
		}
		for _, path := range paths {
			q.Add("path", "color:"+groupColor(path.Group)+"D0|weight:5|enc:"+path.Encoded)
		}
		q.Set("key", key)
		return base + "/maps/api/staticmap?" + q.Encode()
	}

	for _, tolerance := range dayTolerances {
		paths := make([]daymap.Path, 0, len(spec.Paths))
		for _, path := range spec.Paths {
			paths = append(paths, daymap.Path{Encoded: simplifyEncoded(path.Encoded, tolerance), Group: path.Group})
		}
		if full := build(paths); len(full) <= maxStaticURL {
			return full
		}
	}
	// Even the coarsest lines do not fit: keep the stops, which are what matters most.
	return build(nil)
}

// pinZoom is a street-level view: enough to recognise the block, not so close that it is only a roof.
const pinZoom = "15"

func staticPinURL(base, key string, spec place.PinSpec) string {
	q := url.Values{}
	q.Set("size", staticMapSize)
	q.Set("scale", "2")
	q.Set("maptype", "roadmap")
	q.Set("format", "png")
	q.Set("zoom", pinZoom)
	if spec.Language != "" {
		q.Set("language", spec.Language)
	}
	q.Add("markers", "color:"+brandBlueMarker+"|"+point(spec.Location))
	q.Set("key", key)
	return base + "/maps/api/staticmap?" + q.Encode()
}

// RenderPin draws one marker on a street-level map.
func (s *StaticMaps) RenderPin(ctx context.Context, spec place.PinSpec) (kernel.MapImage, error) {
	return s.fetch(ctx, staticPinURL(s.base, s.key, spec))
}

// RenderDay draws every stop of a day, numbered, with the route between each pair.
func (s *StaticMaps) RenderDay(ctx context.Context, spec daymap.DaySpec) (kernel.MapImage, error) {
	return s.fetch(ctx, staticDayURL(s.base, s.key, spec))
}

func (s *StaticMaps) fetch(ctx context.Context, target string) (kernel.MapImage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return kernel.MapImage{}, fmt.Errorf("build static map request: %w", err)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return kernel.MapImage{}, fmt.Errorf("static map request failed: %w", stripURL(err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMapImageBytes))
	if err != nil {
		return kernel.MapImage{}, fmt.Errorf("read static map: %w", err)
	}
	contentType := resp.Header.Get("Content-Type")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(contentType, "image/") {
		return kernel.MapImage{}, fmt.Errorf("google static map responded %d: %s", resp.StatusCode, errorText(data))
	}
	return kernel.MapImage{Data: data, ContentType: contentType}, nil
}

// errorText keeps the start of Google's plain-text explanation ("...not authorized to use this
// API...") which is what tells the owner that an API still has to be enabled.
func errorText(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 240 {
		text = text[:240]
	}
	if text == "" {
		return "empty response"
	}
	return text
}
