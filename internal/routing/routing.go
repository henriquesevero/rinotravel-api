// Package routing is the port to an external routing service: given two places and a way to travel
// between them, how long the trip takes. The day map is what plans a route now; a transfer used to be
// its own booked entity with this same port, but the app no longer models transfers that way.
package routing

import (
	"context"
	"time"

	"rinotravel-api/internal/kernel"
)

// Mode is a request's preferred way of travelling. Only WALKING and CAR are ever asked for; the
// others are what a computed route's own legs come back as (a subway leg, say).
type Mode string

const (
	ModeWalking   Mode = "WALKING"
	ModeSubway    Mode = "SUBWAY"
	ModeTrain     Mode = "TRAIN"
	ModeBus       Mode = "BUS"
	ModeTaxi      Mode = "TAXI"
	ModeRideshare Mode = "RIDESHARE"
	ModeCar       Mode = "CAR"
	ModeOther     Mode = "OTHER"
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
	// Polyline is the provider's encoded line of the whole route, only used to draw a map on demand.
	// It is never stored: the provider's terms do not allow keeping its content.
	Polyline string
}

// RouteProvider is the port to a routing service. The day map never sees Google types directly.
type RouteProvider interface {
	Name() string
	Compute(ctx context.Context, req RouteRequest) ([]Route, error)
}
