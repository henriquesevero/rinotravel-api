package google_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"rinotravel-api/internal/google"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/place"
	"rinotravel-api/internal/quota"
	"rinotravel-api/internal/quota/quotatest"
	"rinotravel-api/internal/transfer"
)

type countingPlaces struct{ searches, details int }

func (c *countingPlaces) Search(context.Context, string, *kernel.Coordinates, string) ([]place.Candidate, error) {
	c.searches++
	return []place.Candidate{{ProviderID: "p1", Name: "Tokyo Tower"}}, nil
}

func (c *countingPlaces) Details(context.Context, string, string) (place.Candidate, error) {
	c.details++
	return place.Candidate{ProviderID: "p1"}, nil
}

type countingRoutes struct{ calls int }

func (c *countingRoutes) Name() string { return "fake" }
func (c *countingRoutes) Compute(context.Context, transfer.RouteRequest) ([]transfer.Route, error) {
	c.calls++
	return []transfer.Route{{ExternalID: "r1"}}, nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestMeteredPlaces_StopsCallingGoogleOnceTheLimitIsUsed(t *testing.T) {
	inner := &countingPlaces{}
	counter := &quotatest.Counter{}
	provider := google.NewMeteredPlaces(inner, counter, 2, quiet)

	for range 2 {
		if _, err := provider.Search(context.Background(), "tokyo", nil, "en"); err != nil {
			t.Fatalf("Search within the limit: %v", err)
		}
	}
	for range 3 {
		if _, err := provider.Search(context.Background(), "tokyo", nil, "en"); !errors.Is(err, quota.ErrExhausted) {
			t.Fatalf("Search past the limit err = %v, want ErrExhausted", err)
		}
	}
	if inner.searches != 2 {
		t.Errorf("Google was called %d times, want exactly the 2 allowed", inner.searches)
	}
}

func TestMeteredPlaces_DetailsShareTheSameAllowance(t *testing.T) {
	inner := &countingPlaces{}
	provider := google.NewMeteredPlaces(inner, &quotatest.Counter{}, 1, quiet)

	if _, err := provider.Details(context.Background(), "p1", "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Search(context.Background(), "tokyo", nil, "en"); !errors.Is(err, quota.ErrExhausted) {
		t.Errorf("Search after Details err = %v, want ErrExhausted", err)
	}
}

func TestMeteredRoutes_CountsSeparatelyFromPlaces(t *testing.T) {
	counter := &quotatest.Counter{}
	places := google.NewMeteredPlaces(&countingPlaces{}, counter, 1, quiet)
	routes := google.NewMeteredRoutes(&countingRoutes{}, counter, 1, quiet)

	if _, err := places.Search(context.Background(), "tokyo", nil, "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := routes.Compute(context.Background(), transfer.RouteRequest{}); err != nil {
		t.Errorf("routes were blocked by the places allowance: %v", err)
	}
	if _, err := routes.Compute(context.Background(), transfer.RouteRequest{}); !errors.Is(err, quota.ErrExhausted) {
		t.Errorf("second route err = %v, want ErrExhausted", err)
	}
	if routes.Name() != "fake" {
		t.Errorf("Name() = %q, want the wrapped provider's name", routes.Name())
	}
}

func TestMetered_RefusesTheCallWhenTheCounterIsBroken(t *testing.T) {
	inner := &countingPlaces{}
	broken := errors.New("mongo is down")
	provider := google.NewMeteredPlaces(inner, &quotatest.Counter{Fail: broken}, 100, quiet)

	_, err := provider.Search(context.Background(), "tokyo", nil, "en")
	if !errors.Is(err, broken) {
		t.Fatalf("err = %v, want the counter's error", err)
	}
	if inner.searches != 0 {
		t.Error("Google was called even though the call could not be counted")
	}
}
