package google

import (
	"reflect"
	"testing"

	"rinotravel-api/internal/kernel"
)

func TestWaypoint_PrefersWordsToTheRawPoint(t *testing.T) {
	point := &kernel.Coordinates{Lat: 40.6446, Lng: -73.7797}
	tests := []struct {
		name string
		in   kernel.Location
		want map[string]any
	}{
		{"name and address are sent as text", kernel.Location{Name: "JFK", Address: "Jamaica, NY", Coordinates: point}, map[string]any{"address": "JFK, Jamaica, NY"}},
		{"an address that already holds the name is not repeated", kernel.Location{Name: "Times Square", Address: "Times Square, Manhattan", Coordinates: point}, map[string]any{"address": "Times Square, Manhattan"}},
		{"address alone", kernel.Location{Address: "Shinjuku, Tokyo"}, map[string]any{"address": "Shinjuku, Tokyo"}},
		{"coordinates when there is no address", kernel.Location{Name: "Somewhere", Coordinates: point}, map[string]any{"location": map[string]any{"latLng": map[string]float64{"latitude": 40.6446, "longitude": -73.7797}}}},
		{"a bare name", kernel.Location{Name: "Aeroporto de Narita"}, map[string]any{"address": "Aeroporto de Narita"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := waypoint(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("waypoint() = %v, want %v", got, tt.want)
			}
		})
	}
}
