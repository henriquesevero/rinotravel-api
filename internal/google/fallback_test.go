package google_test

import (
	"context"
	"testing"

	"rinotravel-api/internal/google"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/transfer"
)

// bareNames finds no route in Portuguese, the way the real service does for a place given only as a name.
type bareNames struct{ languages []string }

func (b *bareNames) Name() string { return "fake" }
func (b *bareNames) Compute(_ context.Context, req transfer.RouteRequest) ([]transfer.Route, error) {
	b.languages = append(b.languages, req.Language)
	if req.Language == "en" {
		return []transfer.Route{{ExternalID: "r1"}}, nil
	}
	return nil, nil
}

func TestEnglishFallback(t *testing.T) {
	named := kernel.Location{Name: "Grand Central"}
	addressed := kernel.Location{Name: "Grand Central", Address: "89 E 42nd St, New York"}
	pinned := kernel.Location{Coordinates: &kernel.Coordinates{Lat: 40.75, Lng: -73.97}}

	tests := []struct {
		name      string
		language  string
		origin    kernel.Location
		wantCalls []string
		wantFound bool
	}{
		{"a bare name is asked again in English", "pt-BR", named, []string{"pt-BR", "en"}, true},
		{"a place with an address is not ambiguous, so no second call", "pt-BR", addressed, []string{"pt-BR"}, false},
		{"a place with coordinates is not ambiguous either", "pt-BR", pinned, []string{"pt-BR"}, false},
		{"English is not asked twice", "en", named, []string{"en"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner := &bareNames{}
			provider := google.NewEnglishFallback(inner)
			routes, err := provider.Compute(context.Background(), transfer.RouteRequest{Origin: tc.origin, Destination: tc.origin, Language: tc.language})
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			if (len(routes) > 0) != tc.wantFound {
				t.Errorf("found a route = %v, want %v", len(routes) > 0, tc.wantFound)
			}
			if len(inner.languages) != len(tc.wantCalls) {
				t.Fatalf("calls in %v, want %v", inner.languages, tc.wantCalls)
			}
			for i, want := range tc.wantCalls {
				if inner.languages[i] != want {
					t.Errorf("call %d in %q, want %q", i, inner.languages[i], want)
				}
			}
		})
	}
}
