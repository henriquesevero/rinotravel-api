// Package transfer models getting from A to B as its own domain: a transfer is an ordered list of
// legs (walk, subway, taxi, ...), not just another itinerary item.
package transfer

import (
	"context"
	"errors"
	"strconv"
	"time"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

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

func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case ModeWalking, ModeSubway, ModeTrain, ModeBus, ModeTaxi, ModeRideshare, ModeCar, ModeOther:
		return m, nil
	}
	return "", errors.New("must be one of WALKING, SUBWAY, TRAIN, BUS, TAXI, RIDESHARE, CAR, OTHER")
}

const (
	maxLegs            = 20
	maxDurationMinutes = 7 * 24 * 60
)

type Leg struct {
	Mode        Mode
	Origin      kernel.Location
	Destination kernel.Location
	Departure   *kernel.ZonedTime
	Arrival     *kernel.ZonedTime
	// DurationMinutes is only an estimate for legs without both times; see EffectiveDuration.
	DurationMinutes *int
	Line            string
	Direction       string
	Stops           *int
	Instructions    string
	Cost            *kernel.Money
}

func (l Leg) EffectiveDuration() (time.Duration, bool) {
	switch {
	case l.Departure != nil && l.Arrival != nil:
		return l.Arrival.Instant.Sub(l.Departure.Instant), true
	case l.DurationMinutes != nil:
		return time.Duration(*l.DurationMinutes) * time.Minute, true
	}
	return 0, false
}

type Transfer struct {
	kernel.Base
	Origin          kernel.Location
	Destination     kernel.Location
	Status          kernel.PlanStatus
	RouteProvider   string
	ExternalRouteID string
	Notes           string
	Legs            []Leg
}

func Base(t *Transfer) *kernel.Base { return &t.Base }

// Departure and Arrival come from the first and last leg that know their time.
func (t Transfer) Departure() *kernel.ZonedTime {
	for _, l := range t.Legs {
		if l.Departure != nil {
			return l.Departure
		}
	}
	return nil
}

func (t Transfer) Arrival() *kernel.ZonedTime {
	for i := len(t.Legs) - 1; i >= 0; i-- {
		if t.Legs[i].Arrival != nil {
			return t.Legs[i].Arrival
		}
	}
	return nil
}

// Duration is the real span when the whole transfer is timed, otherwise the sum of the legs' own
// durations or estimates.
func (t Transfer) Duration() (time.Duration, bool) {
	if dep, arr := t.Departure(), t.Arrival(); dep != nil && arr != nil {
		return arr.Instant.Sub(dep.Instant), true
	}
	var total time.Duration
	var known bool
	for _, l := range t.Legs {
		if d, ok := l.EffectiveDuration(); ok {
			total += d
			known = true
		}
	}
	return total, known
}

func (t Transfer) TotalCost() *kernel.Money {
	costs := make([]*kernel.Money, 0, len(t.Legs))
	for _, l := range t.Legs {
		costs = append(costs, l.Cost)
	}
	total, _ := kernel.SumMoney(costs)
	return total
}

func (t Transfer) Entry() (kernel.TimelineEntry, bool) {
	dep := t.Departure()
	if dep == nil {
		return kernel.TimelineEntry{}, false
	}
	return kernel.TimelineEntry{
		Kind: "transfer", ID: t.ID, Title: routeTitle(t), Status: string(t.Status),
		Start: dep, End: t.Arrival(), Day: dep.LocalDate(),
	}, true
}

func routeTitle(t Transfer) string {
	name := func(l kernel.Location) string {
		if l.Name != "" {
			return l.Name
		}
		return l.Address
	}
	return name(t.Origin) + " → " + name(t.Destination)
}

type LegInput struct {
	Mode            string                 `json:"mode"`
	Origin          kernel.LocationInput   `json:"origin"`
	Destination     kernel.LocationInput   `json:"destination"`
	Departure       *kernel.ZonedTimeInput `json:"departure"`
	Arrival         *kernel.ZonedTimeInput `json:"arrival"`
	DurationMinutes *int                   `json:"estimatedDurationMinutes"`
	Line            string                 `json:"line"`
	Direction       string                 `json:"direction"`
	Stops           *int                   `json:"stops"`
	Instructions    string                 `json:"instructions"`
	Cost            *kernel.MoneyInput     `json:"cost"`
}

type Fields struct {
	Origin          kernel.Optional[kernel.LocationInput] `json:"origin"`
	Destination     kernel.Optional[kernel.LocationInput] `json:"destination"`
	Status          *string                               `json:"status"`
	RouteProvider   *string                               `json:"routeProvider"`
	ExternalRouteID *string                               `json:"externalRouteId"`
	Notes           *string                               `json:"notes"`
	// Legs, when present, replaces the whole list.
	Legs *[]LegInput `json:"legs"`
}

type Create struct {
	ID string `json:"id"`
	Fields
}

type Patch struct {
	kernel.Versioned
	Fields
}

type Transfers struct {
	res *resource.Service[Transfer]
}

func NewTransfers(repo resource.Repo[Transfer], authz resource.Authorizer) *Transfers {
	return &Transfers{res: resource.NewService[Transfer](repo, authz, resource.Config[Transfer]{Name: "transfer", Base: Base})}
}

func (s *Transfers) Resource() *resource.Service[Transfer] { return s.res }

func (s *Transfers) Create(ctx context.Context, actor user.ID, tripID trip.ID, in Create) (resource.Result[Transfer], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(a trip.Access) (Transfer, error) {
		return apply(Transfer{Status: kernel.StatusPlanned}, in.Fields, a, true)
	})
}

func (s *Transfers) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p Patch) (resource.Result[Transfer], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Transfer]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Transfer, a trip.Access) (Transfer, error) {
		return apply(cur, p.Fields, a, false)
	})
}

func apply(cur Transfer, f Fields, access trip.Access, creating bool) (Transfer, error) {
	var v kernel.Validator
	t := cur
	if creating {
		v.Check(f.Legs != nil && len(*f.Legs) > 0, "legs", "at least one leg is required")
	}
	if f.Status != nil {
		status, err := kernel.ParsePlanStatus(*f.Status)
		if err != nil {
			v.Add("status", err.Error())
		}
		t.Status = status
	}
	if f.RouteProvider != nil {
		t.RouteProvider = v.Text("routeProvider", *f.RouteProvider, false, 50)
	}
	if f.ExternalRouteID != nil {
		t.ExternalRouteID = v.Text("externalRouteId", *f.ExternalRouteID, false, 300)
	}
	if f.Notes != nil {
		t.Notes = v.Text("notes", *f.Notes, false, 2000)
	}
	if f.Origin.Set {
		t.Origin = kernel.Location{}
		if !f.Origin.Clear {
			t.Origin = v.Location("origin", f.Origin.Value)
		}
	}
	if f.Destination.Set {
		t.Destination = kernel.Location{}
		if !f.Destination.Clear {
			t.Destination = v.Location("destination", f.Destination.Value)
		}
	}
	if f.Legs != nil {
		t.Legs = buildLegs(&v, *f.Legs, access.Trip.Timezone)
	}
	if v.HasErrors() {
		return Transfer{}, v.Err()
	}

	if len(t.Legs) == 0 {
		v.Add("legs", "at least one leg is required")
		return Transfer{}, v.Err()
	}
	// The transfer's ends default to its first and last leg, so a route is never left unnamed.
	if t.Origin.IsZero() {
		t.Origin = t.Legs[0].Origin
	}
	if t.Destination.IsZero() {
		t.Destination = t.Legs[len(t.Legs)-1].Destination
	}
	checkLegs(&v, t.Legs)
	return t, v.Err()
}

func buildLegs(v *kernel.Validator, inputs []LegInput, tz kernel.Timezone) []Leg {
	v.Check(len(inputs) <= maxLegs, "legs", "must have at most 20 legs")
	legs := make([]Leg, 0, len(inputs))
	for i, in := range inputs {
		field := "legs[" + strconv.Itoa(i) + "]"
		mode, err := ParseMode(in.Mode)
		if err != nil {
			v.Add(field+".mode", err.Error())
		}
		leg := Leg{
			Mode:         mode,
			Origin:       v.Location(field+".origin", in.Origin),
			Destination:  v.Location(field+".destination", in.Destination),
			Departure:    v.ZonedTime(field+".departure", in.Departure, tz),
			Arrival:      v.ZonedTime(field+".arrival", in.Arrival, tz),
			Line:         v.Text(field+".line", in.Line, false, 100),
			Direction:    v.Text(field+".direction", in.Direction, false, 200),
			Instructions: v.Text(field+".instructions", in.Instructions, false, 1000),
			Cost:         v.Money(field+".cost", in.Cost),
		}
		v.Check(!leg.Origin.IsZero(), field+".origin", "is required")
		v.Check(!leg.Destination.IsZero(), field+".destination", "is required")
		if in.DurationMinutes != nil {
			minutes := *in.DurationMinutes
			v.Check(minutes >= 0 && minutes <= maxDurationMinutes, field+".estimatedDurationMinutes", "must be between 0 and 10080")
			leg.DurationMinutes = &minutes
		}
		if in.Stops != nil {
			v.Check(*in.Stops >= 0 && *in.Stops <= 200, field+".stops", "must be between 0 and 200")
			leg.Stops = in.Stops
		}
		legs = append(legs, leg)
	}
	return legs
}

// checkLegs enforces that time only moves forward: within a leg and from one leg to the next.
func checkLegs(v *kernel.Validator, legs []Leg) {
	var currency kernel.Currency
	var previousArrival *kernel.ZonedTime
	for i, l := range legs {
		field := "legs[" + strconv.Itoa(i) + "]"
		if l.Departure != nil && l.Arrival != nil {
			v.Check(!l.Arrival.Before(*l.Departure), field+".arrival.dateTime", "must not be before the departure")
		}
		if l.Departure != nil && previousArrival != nil {
			v.Check(!l.Departure.Before(*previousArrival), field+".departure.dateTime", "must not be before the previous leg arrives")
		}
		if l.Arrival != nil {
			previousArrival = l.Arrival
		}
		if l.Cost != nil {
			if currency != "" && l.Cost.Currency != currency {
				v.Add(field+".cost.currency", "all legs must use the same currency")
			}
			currency = l.Cost.Currency
		}
	}
}

// TimelineSource adds timed transfers to the itinerary timeline.
type TimelineSource struct {
	repo resource.Repo[Transfer]
}

func NewTimelineSource(repo resource.Repo[Transfer]) TimelineSource {
	return TimelineSource{repo: repo}
}

func (s TimelineSource) Entries(ctx context.Context, tripID string) ([]kernel.TimelineEntry, error) {
	transfers, err := s.repo.List(ctx, tripID)
	if err != nil {
		return nil, err
	}
	var out []kernel.TimelineEntry
	for _, t := range transfers {
		if entry, ok := t.Entry(); ok {
			out = append(out, entry)
		}
	}
	return out, nil
}
