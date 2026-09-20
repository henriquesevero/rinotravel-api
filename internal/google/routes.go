package google

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/transfer"
)

const routeFields = "routes.duration,routes.distanceMeters,routes.polyline.encodedPolyline," +
	"routes.legs.steps.travelMode,routes.legs.steps.staticDuration,routes.legs.steps.distanceMeters," +
	"routes.legs.steps.startLocation,routes.legs.steps.endLocation," +
	"routes.legs.steps.navigationInstruction.instructions,routes.legs.steps.transitDetails"

type Routes struct {
	c client
}

func NewRoutes(apiKey string) *Routes {
	return &Routes{c: newClient(apiKey, defaultRoutesBase, nil)}
}

// NewRoutesWithBase points the adapter at another server; tests use it with a fake Google.
func NewRoutesWithBase(apiKey, base string, httpClient *http.Client) *Routes {
	return &Routes{c: newClient(apiKey, base, httpClient)}
}

func (r *Routes) Name() string { return "google" }

type latLng struct {
	LatLng struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"latLng"`
}

type stopDTO struct {
	Name string `json:"name"`
}

type stepDTO struct {
	TravelMode         string `json:"travelMode"`
	StaticDuration     string `json:"staticDuration"`
	StartLocation      latLng `json:"startLocation"`
	EndLocation        latLng `json:"endLocation"`
	NavigationInstruct struct {
		Instructions string `json:"instructions"`
	} `json:"navigationInstruction"`
	Transit *struct {
		StopDetails struct {
			ArrivalStop   stopDTO `json:"arrivalStop"`
			DepartureStop stopDTO `json:"departureStop"`
			ArrivalTime   string  `json:"arrivalTime"`
			DepartureTime string  `json:"departureTime"`
		} `json:"stopDetails"`
		Line struct {
			Name      string `json:"name"`
			NameShort string `json:"nameShort"`
			Vehicle   struct {
				Type string `json:"type"`
			} `json:"vehicle"`
		} `json:"transitLine"`
		Headsign  string `json:"headsign"`
		StopCount int    `json:"stopCount"`
	} `json:"transitDetails"`
}

type routesResponse struct {
	Routes []struct {
		Duration       string `json:"duration"`
		DistanceMeters int    `json:"distanceMeters"`
		Polyline       struct {
			Encoded string `json:"encodedPolyline"`
		} `json:"polyline"`
		Legs []struct {
			Steps []stepDTO `json:"steps"`
		} `json:"legs"`
	} `json:"routes"`
}

func (r *Routes) Compute(ctx context.Context, req transfer.RouteRequest) ([]transfer.Route, error) {
	body := map[string]any{
		"origin":       waypoint(req.Origin),
		"destination":  waypoint(req.Destination),
		"travelMode":   travelMode(req.Mode),
		"languageCode": languageOrDefault(req.Language),
	}
	if req.DepartureAt != nil {
		body["departureTime"] = req.DepartureAt.UTC().Format(time.RFC3339)
	}
	var out routesResponse
	if err := r.c.do(ctx, http.MethodPost, "/directions/v2:computeRoutes", routeFields, body, &out); err != nil {
		return nil, err
	}

	routes := make([]transfer.Route, 0, len(out.Routes))
	for _, raw := range out.Routes {
		route := transfer.Route{Duration: parseDuration(raw.Duration), DistanceMeters: raw.DistanceMeters, Polyline: raw.Polyline.Encoded}
		for _, leg := range raw.Legs {
			route.Legs = append(route.Legs, mergeSteps(leg.Steps)...)
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func languageOrDefault(l string) string {
	if l == "" {
		return "en"
	}
	return l
}

// waypoint prefers the words to the raw point. Google finds a transit route from "John F. Kennedy
// International Airport, Jamaica, NY" but none from the coordinates of that same place (they can fall
// inside a terminal or a park, far from any stop), so a place that has an address is sent by name and
// address. Coordinates are used when there is no text to send.
func waypoint(l kernel.Location) map[string]any {
	name, address := strings.TrimSpace(l.Name), strings.TrimSpace(l.Address)
	switch {
	case address != "" && name != "" && !strings.Contains(address, name):
		return map[string]any{"address": name + ", " + address}
	case address != "":
		return map[string]any{"address": address}
	case l.Coordinates != nil:
		return map[string]any{"location": map[string]any{"latLng": map[string]float64{
			"latitude": l.Coordinates.Lat, "longitude": l.Coordinates.Lng,
		}}}
	default:
		return map[string]any{"address": name}
	}
}

func travelMode(m transfer.Mode) string {
	switch m {
	case transfer.ModeWalking:
		return "WALK"
	case transfer.ModeCar, transfer.ModeTaxi, transfer.ModeRideshare:
		return "DRIVE"
	default:
		return "TRANSIT"
	}
}

// parseDuration reads Google's "123s" duration format.
func parseDuration(s string) time.Duration {
	seconds, err := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
	if err != nil {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	return nil
}

func location(name string, p latLng) kernel.Location {
	return kernel.Location{Name: name, Coordinates: &kernel.Coordinates{Lat: p.LatLng.Latitude, Lng: p.LatLng.Longitude}}
}

func vehicleMode(vehicle string) transfer.Mode {
	switch vehicle {
	case "SUBWAY", "METRO_RAIL":
		return transfer.ModeSubway
	case "BUS", "INTERCITY_BUS", "TROLLEYBUS", "SHARE_TAXI":
		return transfer.ModeBus
	case "RAIL", "HEAVY_RAIL", "COMMUTER_TRAIN", "HIGH_SPEED_TRAIN", "LONG_DISTANCE_TRAIN", "MONORAIL", "TRAM", "LIGHT_RAIL":
		return transfer.ModeTrain
	}
	return transfer.ModeOther
}

// mergeSteps turns Google's fine-grained steps into legs a traveler cares about: consecutive walking
// (or driving) steps collapse into one leg, and every transit ride is its own leg.
func mergeSteps(steps []stepDTO) []transfer.RouteLeg {
	var legs []transfer.RouteLeg
	for _, step := range steps {
		duration := parseDuration(step.StaticDuration)
		if step.Transit != nil {
			t := step.Transit
			stops := t.StopCount
			legs = append(legs, transfer.RouteLeg{
				Mode:        vehicleMode(t.Line.Vehicle.Type),
				Origin:      location(t.StopDetails.DepartureStop.Name, step.StartLocation),
				Destination: location(t.StopDetails.ArrivalStop.Name, step.EndLocation),
				Departure:   parseTime(t.StopDetails.DepartureTime),
				Arrival:     parseTime(t.StopDetails.ArrivalTime),
				Duration:    duration,
				Line:        firstNonEmpty(t.Line.NameShort, t.Line.Name),
				Direction:   t.Headsign,
				Stops:       &stops,
			})
			continue
		}

		mode := transfer.ModeWalking
		if step.TravelMode == "DRIVE" {
			mode = transfer.ModeCar
		}
		if n := len(legs); n > 0 && legs[n-1].Mode == mode && legs[n-1].Departure == nil {
			legs[n-1].Duration += duration
			legs[n-1].Destination = location("", step.EndLocation)
			legs[n-1].Instructions = joinInstructions(legs[n-1].Instructions, step.NavigationInstruct.Instructions)
			continue
		}
		legs = append(legs, transfer.RouteLeg{
			Mode: mode, Origin: location("", step.StartLocation), Destination: location("", step.EndLocation),
			Duration: duration, Instructions: step.NavigationInstruct.Instructions,
		})
	}
	return legs
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

const maxInstructionsLength = 1000

func joinInstructions(existing, next string) string {
	if next == "" {
		return existing
	}
	if existing == "" {
		return next
	}
	joined := existing + " " + next
	if len(joined) > maxInstructionsLength {
		return joined[:maxInstructionsLength]
	}
	return joined
}
