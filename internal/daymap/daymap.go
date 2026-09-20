// Package daymap plans the day on a map: the stops of one day in order, the trip between each pair
// of them and, when asked, a picture with every stop and route on it. The routes come from the
// route provider on request and are handed straight to the client; nothing from the provider is
// stored, because its terms do not allow it.
package daymap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/quota"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/transfer"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

const (
	MaxStops = 25
	// legConcurrency keeps a long day from opening a burst of requests to the route provider.
	legConcurrency = 4
)

// Mode is how the whole day is travelled. Public transit is the default for a city day.
type Mode string

const (
	Transit Mode = "TRANSIT"
	Walking Mode = "WALKING"
	Driving Mode = "DRIVING"
)

func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToUpper(s)); m {
	case "":
		return Transit, nil
	case Transit, Walking, Driving:
		return m, nil
	}
	return "", fmt.Errorf("must be one of TRANSIT, WALKING, DRIVING")
}

func (m Mode) transferMode() transfer.Mode {
	switch m {
	case Walking:
		return transfer.ModeWalking
	case Driving:
		return transfer.ModeCar
	default:
		return "" // the provider's default: public transit with walking
	}
}

type StopInput struct {
	Label    string               `json:"label"`
	Location kernel.LocationInput `json:"location"`
}

type Request struct {
	Stops        []StopInput `json:"stops"`
	Mode         string      `json:"mode"`
	Language     string      `json:"language"`
	IncludeImage bool        `json:"includeImage"`
}

// Leg is the trip from stop From to stop To. Available is false when no route could be found, so the
// client can still show the stops without inventing a time.
type Leg struct {
	From, To        int
	Available       bool
	DurationSeconds int
	DistanceMeters  int
	// Polyline is the provider's encoded line; it is meant to be drawn on the provider's own map.
	Polyline string
}

type Result struct {
	Legs  []Leg
	Image *kernel.MapImage
}

type Marker struct {
	Label    string
	Location kernel.Location
}

// DaySpec is what a picture of the whole day needs.
type DaySpec struct {
	Stops    []Marker
	Paths    []string
	Language string
}

// ImageRenderer is the port to a static map service that can draw a day.
type ImageRenderer interface {
	RenderDay(ctx context.Context, spec DaySpec) (kernel.MapImage, error)
}

type Service struct {
	routes   transfer.RouteProvider
	renderer ImageRenderer
	authz    resource.Authorizer
	logger   *slog.Logger
}

func NewService(routes transfer.RouteProvider, renderer ImageRenderer, authz resource.Authorizer, logger *slog.Logger) *Service {
	return &Service{routes: routes, renderer: renderer, authz: authz, logger: logger}
}

func (s *Service) Plan(ctx context.Context, actor user.ID, tripID trip.ID, in Request) (Result, error) {
	if _, err := s.authz.Authorize(ctx, tripID, actor, trip.ActionRead); err != nil {
		return Result{}, err
	}

	var v kernel.Validator
	v.Check(len(in.Stops) >= 2 && len(in.Stops) <= MaxStops, "stops", fmt.Sprintf("must have between 2 and %d stops", MaxStops))
	mode, err := ParseMode(in.Mode)
	if err != nil {
		v.Add("mode", err.Error())
	}
	stops := make([]Marker, 0, len(in.Stops))
	for i, stop := range in.Stops {
		field := fmt.Sprintf("stops[%d].location", i)
		location := v.Location(field, stop.Location)
		v.Check(location.Coordinates != nil || location.Address != "" || location.Name != "", field, "is required")
		v.Check(len(stop.Label) <= 200, fmt.Sprintf("stops[%d].label", i), "must have at most 200 characters")
		stops = append(stops, Marker{Label: markerLabel(i), Location: location})
	}
	if err := v.Err(); err != nil {
		return Result{}, err
	}

	legs := s.legs(ctx, stops, mode, in.Language)

	result := Result{Legs: legs}
	if in.IncludeImage && s.renderer != nil {
		paths := make([]string, 0, len(legs))
		for _, leg := range legs {
			if leg.Polyline != "" {
				paths = append(paths, leg.Polyline)
			}
		}
		image, err := s.renderer.RenderDay(ctx, DaySpec{Stops: stops, Paths: paths, Language: in.Language})
		switch {
		case errors.Is(err, quota.ErrExhausted):
			s.logger.WarnContext(ctx, "day map picture skipped: monthly limit reached")
		case err != nil:
			s.logger.ErrorContext(ctx, "day map picture failed", slog.Any("error", err))
		default:
			result.Image = &image
		}
	}
	return result, nil
}

// legs asks for every trip between neighbouring stops, a few at a time. A trip that cannot be found
// is reported as unavailable; it never fails the whole day.
func (s *Service) legs(ctx context.Context, stops []Marker, mode Mode, language string) []Leg {
	legs := make([]Leg, len(stops)-1)
	var wg sync.WaitGroup
	slots := make(chan struct{}, legConcurrency)
	for i := range legs {
		legs[i] = Leg{From: i, To: i + 1}
		if samePlace(stops[i].Location, stops[i+1].Location) {
			legs[i].Available = true // no trip at all: the next stop is the same place
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			legs[i] = s.leg(ctx, i, stops[i].Location, stops[i+1].Location, mode, language)
		}()
	}
	wg.Wait()
	return legs
}

func (s *Service) leg(ctx context.Context, from int, origin, destination kernel.Location, mode Mode, language string) Leg {
	leg := Leg{From: from, To: from + 1}
	routes, err := s.routes.Compute(ctx, transfer.RouteRequest{Origin: origin, Destination: destination, Mode: mode.transferMode(), Language: language})
	switch {
	case errors.Is(err, quota.ErrExhausted):
		s.logger.WarnContext(ctx, "day map trip skipped: monthly limit reached")
	case err != nil:
		s.logger.WarnContext(ctx, "day map trip unavailable", slog.Any("error", err))
	case len(routes) > 0:
		route := routes[0]
		leg.Available = true
		leg.DurationSeconds = int(route.Duration.Seconds())
		leg.DistanceMeters = route.DistanceMeters
		leg.Polyline = route.Polyline
	}
	return leg
}

// markerLabel is what the pin shows: 1 to 9, then A, B, C... (the map service takes one character).
func markerLabel(i int) string {
	if i < 9 {
		return fmt.Sprint(i + 1)
	}
	return string(rune('A' + i - 9))
}

func samePlace(a, b kernel.Location) bool {
	if a.Coordinates != nil && b.Coordinates != nil {
		const tolerance = 0.00002 // about two metres
		return abs(a.Coordinates.Lat-b.Coordinates.Lat) < tolerance && abs(a.Coordinates.Lng-b.Coordinates.Lng) < tolerance
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	text := func(l kernel.Location) string { return norm(l.Address) + "|" + norm(l.Name) }
	return text(a) != "|" && text(a) == text(b)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
