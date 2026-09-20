package google

import (
	"math"
	"strings"
	"testing"

	"rinotravel-api/internal/daymap"
	"rinotravel-api/internal/kernel"
)

// The example from Google's polyline documentation.
const googleExample = "_p~iF~ps|U_ulLnnqC_mqNvxq`@"

func TestPolyline_DecodesGoogleTheExampleAndRoundTrips(t *testing.T) {
	points := decodePolyline(googleExample)
	want := []coord{{38.5, -120.2}, {40.7, -120.95}, {43.252, -126.453}}
	if len(points) != len(want) {
		t.Fatalf("decoded %d points, want %d", len(points), len(want))
	}
	for i := range want {
		if math.Abs(points[i].lat-want[i].lat) > 1e-9 || math.Abs(points[i].lng-want[i].lng) > 1e-9 {
			t.Errorf("point %d = %v, want %v", i, points[i], want[i])
		}
	}
	if got := encodePolyline(points); got != googleExample {
		t.Errorf("re-encoded = %q, want %q", got, googleExample)
	}
}

func TestPolyline_IgnoresGarbageInsteadOfPanicking(t *testing.T) {
	for _, bad := range []string{"", "!", "_", "~~~~~~~~"} {
		_ = decodePolyline(bad) // must simply return whatever it can read
	}
}

func TestSimplify_KeepsTheShapeWithFewerPoints(t *testing.T) {
	// A nearly straight line with tiny wiggles collapses; a real corner stays.
	line := []coord{{0, 0}, {0.00001, 0.5}, {-0.00001, 1}, {0, 1.5}, {1, 1.5}}
	got := simplify(line, 0.001)

	if len(got) != 3 || got[0] != line[0] || got[2] != line[len(line)-1] {
		t.Errorf("simplified = %v, want the two ends and the corner", got)
	}
	if same := simplify(line, 0); len(same) != len(line) {
		t.Errorf("a zero tolerance must keep every point, got %d", len(same))
	}
}

func daySpec(paths ...string) daymap.DaySpec {
	lines := make([]daymap.Path, 0, len(paths))
	for i, p := range paths {
		lines = append(lines, daymap.Path{Encoded: p, Group: i})
	}
	return daymap.DaySpec{
		Stops: []daymap.Marker{
			{Label: "1", Location: kernel.Location{Name: "A", Coordinates: &kernel.Coordinates{Lat: 40.1, Lng: -73.1}}},
			{Label: "2", Location: kernel.Location{Address: "Times Sq, New York"}},
			{Label: "3", Location: kernel.Location{Name: "C"}},
		},
		Paths:    lines,
		Language: "pt-BR",
	}
}

func TestStaticDayURL_NumbersEveryStopAndDrawsEachLeg(t *testing.T) {
	raw := staticDayURL("https://maps.example", "the-key", daySpec(googleExample, googleExample))
	if strings.Count(raw, "label%3A") != 3 || strings.Count(raw, "&path=")+strings.Count(raw, "?path=") != 2 {
		t.Errorf("url = %s", raw)
	}
	for _, label := range []string{"label%3A1%7C", "label%3A2%7C", "label%3A3%7C"} {
		if !strings.Contains(raw, label) {
			t.Errorf("missing marker %s in %s", label, raw)
		}
	}
	if !strings.Contains(raw, "key=the-key") {
		t.Error("the key is missing")
	}
}

func TestStaticDayURL_FitsLongRoutesByCoarseningThem(t *testing.T) {
	// A long wiggly route: thousands of points that would never fit a URL as they are.
	var points []coord
	for i := 0; i < 6000; i++ {
		points = append(points, coord{40 + float64(i)*0.0005, -73 + math.Sin(float64(i)/7)*0.002})
	}
	long := encodePolyline(points)
	raw := staticDayURL("https://maps.example", "k", daySpec(long, long))

	if len(raw) > maxStaticURL {
		t.Fatalf("url has %d characters, over the %d limit", len(raw), maxStaticURL)
	}
	if !strings.Contains(raw, "path=") {
		t.Error("a route that can be coarsened to fit must still be drawn")
	}
	if strings.Count(raw, "label%3A") != 3 {
		t.Error("the stops must always be kept")
	}
}

func TestStaticDayURL_FallsBackToStopsWhenNothingElseFits(t *testing.T) {
	var many []string
	for i := 0; i < 30; i++ {
		many = append(many, googleExample)
	}
	spec := daySpec(many...)
	// Force the limit past what even a coarse line set can meet by making stops themselves long.
	spec.Stops[1].Location.Address = strings.Repeat("Avenida Paulista, São Paulo ", 40)
	raw := staticDayURL("https://maps.example", "k", spec)
	if strings.Contains(raw, "path=") && len(raw) > maxStaticURL {
		t.Errorf("url of %d characters is over the limit", len(raw))
	}
}

func TestStaticDayURL_ColoursEachDayAndAllowsUnlabelledPins(t *testing.T) {
	spec := daymap.DaySpec{
		Stops: []daymap.Marker{
			{Label: "1", Group: 0, Location: kernel.Location{Name: "A"}},
			{Label: "2", Group: 1, Location: kernel.Location{Name: "B"}},
			{Label: "", Group: 9, Location: kernel.Location{Name: "C"}}, // past the last colour: it wraps
		},
		Paths: []daymap.Path{{Encoded: googleExample, Group: 1}},
	}
	raw := staticDayURL("https://maps.example", "k", spec)

	for _, want := range []string{"color%3A0x2563EB%7Clabel%3A1%7CA", "color%3A0xF97316%7Clabel%3A2%7CB", "color%3A0xF97316D0%7Cweight%3A5"} {
		if !strings.Contains(raw, want) {
			t.Errorf("missing %q in %s", want, raw)
		}
	}
	// Group 9 wraps to the second colour, and a pin without a label is still a pin.
	if !strings.Contains(raw, "color%3A0xF97316%7CC") || strings.Contains(raw, "label%3A%7C") {
		t.Errorf("unlabelled pin wrongly drawn: %s", raw)
	}
}
