package transfer

import (
	"context"
	"log/slog"
	"time"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type RouteRequest struct {
	Origin      kernel.Location
	Destination kernel.Location
	// Mode narrows the search; empty means public transit with walking.
	Mode        Mode
	DepartureAt *time.Time
	Language    string
}

type RouteLeg struct {
	Mode         Mode
	Origin       kernel.Location
	Destination  kernel.Location
	Departure    *time.Time
	Arrival      *time.Time
	Duration     time.Duration
	Line         string
	Direction    string
	Stops        *int
	Instructions string
}

type Route struct {
	ExternalID     string
	Duration       time.Duration
	DistanceMeters int
	Legs           []RouteLeg
}

// RouteProvider is the port to a routing service. The transfer use cases never see Google types.
type RouteProvider interface {
	Name() string
	Compute(ctx context.Context, req RouteRequest) ([]Route, error)
}

type PlanRequest struct {
	Origin      kernel.LocationInput   `json:"origin"`
	Destination kernel.LocationInput   `json:"destination"`
	Mode        string                 `json:"mode"`
	DepartureAt *kernel.ZonedTimeInput `json:"departureAt"`
	Language    string                 `json:"language"`
}

// Draft is a route ready to be saved: Transfer has the same shape as a create request, so the
// client can review it and POST it as is.
type Draft struct {
	DurationMinutes int
	DistanceMeters  int
	Transfer        Create
}

type Planner struct {
	provider RouteProvider
	authz    resource.Authorizer
	logger   *slog.Logger
}

func NewPlanner(provider RouteProvider, authz resource.Authorizer, logger *slog.Logger) *Planner {
	return &Planner{provider: provider, authz: authz, logger: logger}
}

func (p *Planner) Plan(ctx context.Context, actor user.ID, tripID trip.ID, in PlanRequest) ([]Draft, error) {
	access, err := p.authz.Authorize(ctx, tripID, actor, trip.ActionWriteContent)
	if err != nil {
		return nil, err
	}
	tz := access.Trip.Timezone

	var v kernel.Validator
	origin := v.Location("origin", in.Origin)
	destination := v.Location("destination", in.Destination)
	v.Check(origin.Coordinates != nil || origin.Address != "" || origin.Name != "", "origin", "is required")
	v.Check(destination.Coordinates != nil || destination.Address != "" || destination.Name != "", "destination", "is required")
	var mode Mode
	if in.Mode != "" {
		var err error
		if mode, err = ParseMode(in.Mode); err != nil {
			v.Add("mode", err.Error())
		}
	}
	var departure *time.Time
	if at := v.ZonedTime("departureAt", in.DepartureAt, tz); at != nil {
		departure = &at.Instant
	}
	if err := v.Err(); err != nil {
		return nil, err
	}

	routes, err := p.provider.Compute(ctx, RouteRequest{Origin: origin, Destination: destination, Mode: mode, DepartureAt: departure, Language: in.Language})
	if err != nil {
		p.logger.ErrorContext(ctx, "route planning failed", slog.Any("error", err))
		return nil, apperror.Unavailable("provider_unavailable", "Route planning is temporarily unavailable.")
	}

	drafts := make([]Draft, 0, len(routes))
	for _, r := range routes {
		drafts = append(drafts, draft(p.provider.Name(), r, origin, destination, tz))
	}
	return drafts, nil
}

func draft(provider string, r Route, origin, destination kernel.Location, tz kernel.Timezone) Draft {
	legs := make([]LegInput, 0, len(r.Legs))
	for _, l := range r.Legs {
		minutes := int((l.Duration + 30*time.Second) / time.Minute)
		leg := LegInput{
			Mode: string(l.Mode), Origin: locationInput(l.Origin), Destination: locationInput(l.Destination),
			Line: l.Line, Direction: l.Direction, Stops: l.Stops, Instructions: l.Instructions,
			DurationMinutes: &minutes,
		}
		if l.Departure != nil && l.Arrival != nil {
			leg.Departure, leg.Arrival = localInput(*l.Departure, tz), localInput(*l.Arrival, tz)
			leg.DurationMinutes = nil
		}
		legs = append(legs, leg)
	}
	externalID := r.ExternalID
	return Draft{
		DurationMinutes: int(r.Duration / time.Minute),
		DistanceMeters:  r.DistanceMeters,
		Transfer: Create{Fields: Fields{
			Origin:          kernel.Optional[kernel.LocationInput]{Set: true, Value: locationInput(origin)},
			Destination:     kernel.Optional[kernel.LocationInput]{Set: true, Value: locationInput(destination)},
			RouteProvider:   &provider,
			ExternalRouteID: &externalID,
			Legs:            &legs,
		}},
	}
}

func localInput(at time.Time, tz kernel.Timezone) *kernel.ZonedTimeInput {
	z := kernel.ZonedTime{Instant: at.UTC(), Zone: tz}
	return &kernel.ZonedTimeInput{DateTime: z.LocalString(), Timezone: string(tz)}
}

func locationInput(l kernel.Location) kernel.LocationInput {
	in := kernel.LocationInput{Name: l.Name, Address: l.Address}
	if l.Coordinates != nil {
		lat, lng := l.Coordinates.Lat, l.Coordinates.Lng
		in.Latitude, in.Longitude = &lat, &lng
	}
	return in
}
