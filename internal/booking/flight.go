// Package booking holds reservations that have a place in time: flights and hotel stays.
package booking

import (
	"context"
	"regexp"
	"strings"
	"time"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

var (
	iataPattern         = regexp.MustCompile(`^[A-Z]{3}$`)
	flightNumberPattern = regexp.MustCompile(`^[A-Z0-9]{2}[A-Z0-9]?[0-9]{1,4}[A-Z]?$`)
)

const maxFlightDuration = 48 * time.Hour

type Flight struct {
	kernel.Base
	Airline          string
	FlightNumber     string
	DepartureAirport string
	ArrivalAirport   string
	Departure        kernel.ZonedTime
	Arrival          kernel.ZonedTime
	Terminal         string
	Gate             string
	Seat             string
	Baggage          string
	// BookingCode is sensitive and hidden from viewers.
	BookingCode string
	// Cost is the price of the ticket; it counts in the trip's expenses.
	Cost  *kernel.Money
	Notes string
}

func FlightBase(f *Flight) *kernel.Base { return &f.Base }

// Duration is derived from the two instants, so it is right across time zones.
func (f Flight) Duration() time.Duration {
	return f.Arrival.Instant.Sub(f.Departure.Instant)
}

func (f Flight) Entries() []kernel.TimelineEntry {
	title := f.FlightNumber + " " + f.DepartureAirport + " → " + f.ArrivalAirport
	dep, arr := f.Departure, f.Arrival
	entries := []kernel.TimelineEntry{{
		Kind: "flight_departure", ID: f.ID, Title: title, Subtitle: f.Airline,
		Start: &dep, End: &arr, Day: dep.LocalDate(),
	}}
	if arr.LocalDate() != dep.LocalDate() {
		entries = append(entries, kernel.TimelineEntry{
			Kind: "flight_arrival", ID: f.ID, Title: title, Subtitle: f.Airline, Start: &arr, Day: arr.LocalDate(),
		})
	}
	return entries
}

type FlightFields struct {
	Airline          *string                            `json:"airline"`
	FlightNumber     *string                            `json:"flightNumber"`
	DepartureAirport *string                            `json:"departureAirport"`
	ArrivalAirport   *string                            `json:"arrivalAirport"`
	Departure        *kernel.ZonedTimeInput             `json:"departure"`
	Arrival          *kernel.ZonedTimeInput             `json:"arrival"`
	Terminal         *string                            `json:"terminal"`
	Gate             *string                            `json:"gate"`
	Seat             *string                            `json:"seat"`
	Baggage          *string                            `json:"baggage"`
	BookingCode      *string                            `json:"bookingCode"`
	Cost             kernel.Optional[kernel.MoneyInput] `json:"cost"`
	Notes            *string                            `json:"notes"`
}

type FlightCreate struct {
	ID string `json:"id"`
	FlightFields
}

type FlightPatch struct {
	kernel.Versioned
	FlightFields
}

type Flights struct {
	res *resource.Service[Flight]
}

func NewFlights(repo resource.Repo[Flight], authz resource.Authorizer) *Flights {
	return &Flights{res: resource.NewService[Flight](repo, authz, resource.Config[Flight]{Name: "flight", Base: FlightBase})}
}

func (s *Flights) Resource() *resource.Service[Flight] { return s.res }

func (s *Flights) Create(ctx context.Context, actor user.ID, tripID trip.ID, in FlightCreate) (resource.Result[Flight], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(trip.Access) (Flight, error) {
		return applyFlight(Flight{}, in.FlightFields, true)
	})
}

func (s *Flights) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p FlightPatch) (resource.Result[Flight], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Flight]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Flight, _ trip.Access) (Flight, error) {
		return applyFlight(cur, p.FlightFields, false)
	})
}

// flightTime requires an explicit zone: a flight crosses zones, so guessing the trip's would
// silently corrupt the duration.
func flightTime(v *kernel.Validator, field string, in *kernel.ZonedTimeInput, current kernel.ZonedTime) kernel.ZonedTime {
	if in == nil {
		return current
	}
	if in.Timezone == "" {
		v.Add(field+".timezone", "is required for flights")
		return current
	}
	if parsed := v.ZonedTime(field, in, ""); parsed != nil {
		return *parsed
	}
	return current
}

func airport(v *kernel.Validator, field, value string) string {
	code := strings.ToUpper(strings.TrimSpace(value))
	v.Check(iataPattern.MatchString(code), field, "must be a 3-letter IATA airport code")
	return code
}

func applyFlight(cur Flight, f FlightFields, creating bool) (Flight, error) {
	var v kernel.Validator
	fl := cur
	if creating {
		v.Check(f.FlightNumber != nil, "flightNumber", "is required")
		v.Check(f.DepartureAirport != nil, "departureAirport", "is required")
		v.Check(f.ArrivalAirport != nil, "arrivalAirport", "is required")
		v.Check(f.Departure != nil, "departure", "is required")
		v.Check(f.Arrival != nil, "arrival", "is required")
	}
	if f.Airline != nil {
		fl.Airline = v.Text("airline", *f.Airline, false, 100)
	}
	if f.FlightNumber != nil {
		number := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(*f.FlightNumber), " ", ""))
		v.Check(flightNumberPattern.MatchString(number), "flightNumber", "must look like LA8084")
		fl.FlightNumber = number
	}
	if f.DepartureAirport != nil {
		fl.DepartureAirport = airport(&v, "departureAirport", *f.DepartureAirport)
	}
	if f.ArrivalAirport != nil {
		fl.ArrivalAirport = airport(&v, "arrivalAirport", *f.ArrivalAirport)
	}
	fl.Departure = flightTime(&v, "departure", f.Departure, fl.Departure)
	fl.Arrival = flightTime(&v, "arrival", f.Arrival, fl.Arrival)
	if f.Terminal != nil {
		fl.Terminal = v.Text("terminal", *f.Terminal, false, 20)
	}
	if f.Gate != nil {
		fl.Gate = v.Text("gate", *f.Gate, false, 20)
	}
	if f.Seat != nil {
		fl.Seat = v.Text("seat", *f.Seat, false, 20)
	}
	if f.Baggage != nil {
		fl.Baggage = v.Text("baggage", *f.Baggage, false, 200)
	}
	if f.BookingCode != nil {
		fl.BookingCode = v.Text("bookingCode", *f.BookingCode, false, 50)
	}
	if f.Cost.Set {
		fl.Cost = nil
		if !f.Cost.Clear {
			fl.Cost = v.Money("cost", &f.Cost.Value)
		}
	}
	if f.Notes != nil {
		fl.Notes = v.Text("notes", *f.Notes, false, 2000)
	}
	if !v.HasErrors() {
		v.Check(fl.DepartureAirport != fl.ArrivalAirport, "arrivalAirport", "must differ from the departure airport")
		v.Check(fl.Arrival.Instant.After(fl.Departure.Instant), "arrival.dateTime", "must be after the departure")
		v.Check(fl.Duration() <= maxFlightDuration, "arrival.dateTime", "implies a flight longer than 48 hours")
	}
	return fl, v.Err()
}
