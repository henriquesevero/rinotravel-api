package transfer

import (
	"context"
	"errors"
	"log/slog"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/quota"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

// MapSpec is what a map picture needs: the two ends and, when known, the line between them.
type MapSpec struct {
	Origin      kernel.Location
	Destination kernel.Location
	// Polyline is the encoded route line; empty draws only the two markers.
	Polyline string
	Language string
}

// MapImage is shared with the other features that draw maps.
type MapImage = kernel.MapImage

// MapRenderer is the port to a static map service. Pictures are made on request and handed straight
// to the client: nothing from the provider is stored.
type MapRenderer interface {
	Render(ctx context.Context, spec MapSpec) (MapImage, error)
}

type MapRequest struct {
	Origin      kernel.LocationInput `json:"origin"`
	Destination kernel.LocationInput `json:"destination"`
	Mode        string               `json:"mode"`
	Language    string               `json:"language"`
}

// Maps draws the route of a transfer that may not be saved yet, so the form can show it while it is
// being filled in, and the detail view can show it later.
type Maps struct {
	routes   RouteProvider
	renderer MapRenderer
	authz    resource.Authorizer
	logger   *slog.Logger
}

func NewMaps(routes RouteProvider, renderer MapRenderer, authz resource.Authorizer, logger *slog.Logger) *Maps {
	return &Maps{routes: routes, renderer: renderer, authz: authz, logger: logger}
}

func (m *Maps) Render(ctx context.Context, actor user.ID, tripID trip.ID, in MapRequest) (MapImage, error) {
	if _, err := m.authz.Authorize(ctx, tripID, actor, trip.ActionRead); err != nil {
		return MapImage{}, err
	}

	var v kernel.Validator
	origin := v.Location("origin", in.Origin)
	destination := v.Location("destination", in.Destination)
	v.Check(origin.Coordinates != nil || origin.Address != "" || origin.Name != "", "origin", "is required")
	v.Check(destination.Coordinates != nil || destination.Address != "" || destination.Name != "", "destination", "is required")
	var mode Mode
	if in.Mode != "" {
		parsed, err := ParseMode(in.Mode)
		if err != nil {
			v.Add("mode", err.Error())
		}
		mode = parsed
	}
	if err := v.Err(); err != nil {
		return MapImage{}, err
	}

	spec := MapSpec{Origin: origin, Destination: destination, Language: in.Language}
	// The line is a bonus: if the route lookup fails or has run out of allowance, the two markers
	// are still worth showing.
	routes, err := m.routes.Compute(ctx, RouteRequest{Origin: origin, Destination: destination, Mode: mode, Language: in.Language})
	switch {
	case err == nil && len(routes) > 0:
		spec.Polyline = routes[0].Polyline
	case err != nil && !errors.Is(err, quota.ErrExhausted):
		m.logger.WarnContext(ctx, "route line unavailable for the map", slog.Any("error", err))
	}

	image, err := m.renderer.Render(ctx, spec)
	if errors.Is(err, quota.ErrExhausted) {
		return MapImage{}, apperror.Unavailable("provider_quota_exhausted", "The monthly limit of map pictures was reached. It resets next month.")
	}
	if err != nil {
		m.logger.ErrorContext(ctx, "map rendering failed", slog.Any("error", err))
		return MapImage{}, apperror.Unavailable("provider_unavailable", "The map is temporarily unavailable.")
	}
	return image, nil
}
