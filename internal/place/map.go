package place

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

// PinSpec is what a one-place map needs.
type PinSpec struct {
	Location kernel.Location
	Language string
}

// PinRenderer is the port to a static map service that can mark one place.
type PinRenderer interface {
	RenderPin(ctx context.Context, spec PinSpec) (kernel.MapImage, error)
}

type PinRequest struct {
	Location kernel.LocationInput `json:"location"`
	Language string               `json:"language"`
}

// LocationMaps draws a map of a single place: the spot of an itinerary item, a wishlist place, a
// restaurant or a hotel, in a form that is still being filled in or in a saved record.
type LocationMaps struct {
	renderer PinRenderer
	authz    resource.Authorizer
	logger   *slog.Logger
}

func NewLocationMaps(renderer PinRenderer, authz resource.Authorizer, logger *slog.Logger) *LocationMaps {
	return &LocationMaps{renderer: renderer, authz: authz, logger: logger}
}

func (m *LocationMaps) Render(ctx context.Context, actor user.ID, tripID trip.ID, in PinRequest) (kernel.MapImage, error) {
	if _, err := m.authz.Authorize(ctx, tripID, actor, trip.ActionRead); err != nil {
		return kernel.MapImage{}, err
	}

	var v kernel.Validator
	location := v.Location("location", in.Location)
	v.Check(location.Coordinates != nil || location.Address != "" || location.Name != "", "location", "is required")
	if err := v.Err(); err != nil {
		return kernel.MapImage{}, err
	}

	image, err := m.renderer.RenderPin(ctx, PinSpec{Location: location, Language: in.Language})
	if errors.Is(err, quota.ErrExhausted) {
		return kernel.MapImage{}, apperror.Unavailable("provider_quota_exhausted", "The monthly limit of map pictures was reached. It resets next month.")
	}
	if err != nil {
		m.logger.ErrorContext(ctx, "map rendering failed", slog.Any("error", err))
		return kernel.MapImage{}, apperror.Unavailable("provider_unavailable", "The map is temporarily unavailable.")
	}
	return image, nil
}
