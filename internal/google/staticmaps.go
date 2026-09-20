package google

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/transfer"
)

const (
	defaultStaticMapsBase = "https://maps.googleapis.com"
	// maxStaticURL keeps the request under Google's URL limit. A route line long enough to pass it is
	// dropped and the map falls back to the two markers.
	maxStaticURL     = 8000
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

func staticMapQuery(spec transfer.MapSpec, withPath bool) url.Values {
	q := url.Values{}
	q.Set("size", staticMapSize)
	q.Set("scale", "2")
	q.Set("maptype", "roadmap")
	q.Set("format", "png")
	if spec.Language != "" {
		q.Set("language", spec.Language)
	}
	q.Add("markers", "color:"+brandBlueMarker+"|label:A|"+point(spec.Origin))
	q.Add("markers", "color:"+brandBlueMarker+"|label:B|"+point(spec.Destination))
	if withPath && spec.Polyline != "" {
		q.Set("path", "color:"+brandBlueMarker+"FF|weight:5|enc:"+spec.Polyline)
	}
	return q
}

// staticMapURL is separate so the size rule and the encoding can be tested without a network.
func staticMapURL(base, key string, spec transfer.MapSpec) string {
	build := func(withPath bool) string {
		q := staticMapQuery(spec, withPath)
		q.Set("key", key)
		return base + "/maps/api/staticmap?" + q.Encode()
	}
	full := build(true)
	if len(full) <= maxStaticURL {
		return full
	}
	return build(false)
}

func (s *StaticMaps) Render(ctx context.Context, spec transfer.MapSpec) (transfer.MapImage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, staticMapURL(s.base, s.key, spec), nil)
	if err != nil {
		return transfer.MapImage{}, fmt.Errorf("build static map request: %w", err)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return transfer.MapImage{}, fmt.Errorf("static map request failed: %w", stripURL(err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMapImageBytes))
	if err != nil {
		return transfer.MapImage{}, fmt.Errorf("read static map: %w", err)
	}
	contentType := resp.Header.Get("Content-Type")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(contentType, "image/") {
		return transfer.MapImage{}, fmt.Errorf("google static map responded %d: %s", resp.StatusCode, errorText(data))
	}
	return transfer.MapImage{Data: data, ContentType: contentType}, nil
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
