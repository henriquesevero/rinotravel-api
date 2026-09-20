package google

import (
	"context"
	"errors"
	"log/slog"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/place"
	"rinotravel-api/internal/quota"
	"rinotravel-api/internal/transfer"
)

// Buckets are separate because Google's free allowance is per API, not shared.
const (
	BucketPlaces = "google_places"
	BucketRoutes = "google_routes"
)

// meter takes one call from the monthly allowance before anything is sent to Google. A call is
// counted before it is made and never refunded: a request that fails after leaving can still be
// billed, so counting only successes could let the real usage pass the limit.
//
// If the counter itself is unavailable the call is refused. Failing open would mean an outage of
// our own database silently lifts the spending ceiling.
type meter struct {
	counter quota.Counter
	bucket  string
	limit   int
	logger  *slog.Logger
}

func (m meter) take(ctx context.Context) error {
	used, err := m.counter.Take(ctx, m.bucket, m.limit)
	switch {
	case errors.Is(err, quota.ErrExhausted):
		m.logger.WarnContext(ctx, "monthly provider limit reached, call refused",
			slog.String("bucket", m.bucket), slog.Int("limit", m.limit))
		return err
	case err != nil:
		return err
	}
	// One warning as the allowance runs low, and one when it is used up, so it is visible in the logs.
	if used*10 == m.limit*8 {
		m.logger.WarnContext(ctx, "monthly provider limit is 80% used",
			slog.String("bucket", m.bucket), slog.Int("used", used), slog.Int("limit", m.limit))
	}
	return nil
}

// MeteredPlaces counts every call of a place provider against a monthly limit.
type MeteredPlaces struct {
	inner place.PlaceProvider
	meter meter
}

func NewMeteredPlaces(inner place.PlaceProvider, counter quota.Counter, limit int, logger *slog.Logger) *MeteredPlaces {
	return &MeteredPlaces{inner: inner, meter: meter{counter: counter, bucket: BucketPlaces, limit: limit, logger: logger}}
}

func (p *MeteredPlaces) Search(ctx context.Context, query string, near *kernel.Coordinates, language string) ([]place.Candidate, error) {
	if err := p.meter.take(ctx); err != nil {
		return nil, err
	}
	return p.inner.Search(ctx, query, near, language)
}

func (p *MeteredPlaces) Details(ctx context.Context, providerID, language string) (place.Candidate, error) {
	if err := p.meter.take(ctx); err != nil {
		return place.Candidate{}, err
	}
	return p.inner.Details(ctx, providerID, language)
}

// MeteredRoutes counts every call of a route provider against a monthly limit.
type MeteredRoutes struct {
	inner transfer.RouteProvider
	meter meter
}

func NewMeteredRoutes(inner transfer.RouteProvider, counter quota.Counter, limit int, logger *slog.Logger) *MeteredRoutes {
	return &MeteredRoutes{inner: inner, meter: meter{counter: counter, bucket: BucketRoutes, limit: limit, logger: logger}}
}

func (r *MeteredRoutes) Name() string { return r.inner.Name() }

func (r *MeteredRoutes) Compute(ctx context.Context, req transfer.RouteRequest) ([]transfer.Route, error) {
	if err := r.meter.take(ctx); err != nil {
		return nil, err
	}
	return r.inner.Compute(ctx, req)
}
