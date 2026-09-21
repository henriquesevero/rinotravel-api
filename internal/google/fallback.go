package google

import (
	"context"
	"strings"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/transfer"
)

// EnglishFallback asks again in English when a route request came back empty because a place was
// only a name.
//
// The route service reads a bare name such as "Grand Central" through the language of the request:
// in Portuguese it looks for the place near Brazil and finds no route, in English it finds the one in
// New York. A place with an address or coordinates is not ambiguous, so an empty answer for those is
// a real "no route" and is not asked again. It wraps the metered provider, so the second call is
// counted against the monthly limit like any other.
type EnglishFallback struct {
	inner transfer.RouteProvider
}

func NewEnglishFallback(inner transfer.RouteProvider) *EnglishFallback {
	return &EnglishFallback{inner: inner}
}

func (r *EnglishFallback) Name() string { return r.inner.Name() }

func (r *EnglishFallback) Compute(ctx context.Context, req transfer.RouteRequest) ([]transfer.Route, error) {
	routes, err := r.inner.Compute(ctx, req)
	if err != nil || len(routes) > 0 || !retryableLanguage(req.Language) || (!nameOnly(req.Origin) && !nameOnly(req.Destination)) {
		return routes, err
	}
	req.Language = "en"
	return r.inner.Compute(ctx, req)
}

func retryableLanguage(language string) bool {
	language = strings.ToLower(strings.TrimSpace(language))
	return language != "" && !strings.HasPrefix(language, "en")
}

// nameOnly is a place with nothing but a name to find it by.
func nameOnly(l kernel.Location) bool {
	return l.Coordinates == nil && strings.TrimSpace(l.Address) == "" && strings.TrimSpace(l.Name) != ""
}
