package daymap

import (
	"testing"

	"rinotravel-api/internal/kernel"
)

func TestMarkerLabel_UsesDigitsThenLetters(t *testing.T) {
	for i, want := range map[int]string{0: "1", 8: "9", 9: "A", 10: "B", 24: "P", 34: "Z", 35: "", 60: ""} {
		if got := markerLabel(i); got != want {
			t.Errorf("markerLabel(%d) = %q, want %q", i, got, want)
		}
	}
}

func TestSamePlace(t *testing.T) {
	point := func(lat, lng float64) kernel.Location {
		return kernel.Location{Coordinates: &kernel.Coordinates{Lat: lat, Lng: lng}}
	}
	tests := []struct {
		name string
		a, b kernel.Location
		want bool
	}{
		{"the same spot within a couple of metres", point(40.7580, -73.9855), point(40.75801, -73.98551), true},
		{"a block away is another place", point(40.7580, -73.9855), point(40.7590, -73.9855), false},
		{"the same words, whatever the case", kernel.Location{Name: "Katz's", Address: "205 E Houston St"}, kernel.Location{Name: "KATZ'S", Address: "205 e houston st "}, true},
		{"different words", kernel.Location{Name: "Katz's"}, kernel.Location{Name: "Joe's Pizza"}, false},
		{"two empty places are not the same place", kernel.Location{}, kernel.Location{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := samePlace(tt.a, tt.b); got != tt.want {
				t.Errorf("samePlace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": Transit, "transit": Transit, "WALKING": Walking, "driving": Driving} {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	if _, err := ParseMode("JETPACK"); err == nil {
		t.Error("an unknown mode must be refused")
	}
}
