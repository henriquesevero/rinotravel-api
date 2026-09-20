package itinerary

import (
	"context"
	"errors"
	"time"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type Category string

const (
	CategoryRestaurant Category = "RESTAURANT"
	CategoryAttraction Category = "ATTRACTION"
	CategoryShopping   Category = "SHOPPING"
	CategoryFreeTime   Category = "FREE_TIME"
	CategoryOther      Category = "OTHER"
)

func ParseCategory(s string) (Category, error) {
	switch c := Category(s); c {
	case CategoryRestaurant, CategoryAttraction, CategoryShopping, CategoryFreeTime, CategoryOther:
		return c, nil
	}
	return "", errors.New("must be one of RESTAURANT, ATTRACTION, SHOPPING, FREE_TIME, OTHER")
}

const maxDurationMinutes = 7 * 24 * 60

// Item is a manual entry of the plan. Flights, hotels and transfers are their own entities and
// join the timeline through EntrySource instead of being duplicated here.
type Item struct {
	kernel.Base
	DayID       string
	Title       string
	Description string
	Category    Category
	Start       *kernel.ZonedTime
	End         *kernel.ZonedTime
	Location    kernel.Location
	// DurationMinutes is only an estimate for items with no end time; see EffectiveDuration.
	DurationMinutes *int
	Cost            *kernel.Money
	Notes           string
	Status          kernel.PlanStatus
	// Position orders items that share a start time or have none.
	Position int
	PlaceID  string
}

func ItemBase(i *Item) *kernel.Base { return &i.Base }

// EffectiveDuration is derived, never stored twice: the real span when both ends are known,
// otherwise the estimate.
func (i Item) EffectiveDuration() (time.Duration, bool) {
	switch {
	case i.Start != nil && i.End != nil:
		return i.End.Instant.Sub(i.Start.Instant), true
	case i.DurationMinutes != nil:
		return time.Duration(*i.DurationMinutes) * time.Minute, true
	}
	return 0, false
}

func (i Item) Entry(dayDate kernel.Date) kernel.TimelineEntry {
	return kernel.TimelineEntry{
		Kind:     "itinerary_item",
		ID:       i.ID,
		Title:    i.Title,
		Subtitle: string(i.Category),
		Status:   string(i.Status),
		Start:    i.Start,
		End:      i.End,
		Day:      dayDate,
		Position: i.Position,
	}
}

// ItemFields are the editable fields. Absent means "leave as is"; creating requires the
// mandatory ones.
type ItemFields struct {
	DayID           *string                                `json:"dayId"`
	Title           *string                                `json:"title"`
	Description     *string                                `json:"description"`
	Category        *string                                `json:"category"`
	Status          *string                                `json:"status"`
	Notes           *string                                `json:"notes"`
	PlaceID         *string                                `json:"placeId"`
	Position        *int                                   `json:"position"`
	Start           kernel.Optional[kernel.ZonedTimeInput] `json:"start"`
	End             kernel.Optional[kernel.ZonedTimeInput] `json:"end"`
	Location        kernel.Optional[kernel.LocationInput]  `json:"location"`
	DurationMinutes kernel.Optional[int]                   `json:"estimatedDurationMinutes"`
	Cost            kernel.Optional[kernel.MoneyInput]     `json:"estimatedCost"`
}

type ItemCreate struct {
	ID string `json:"id"`
	ItemFields
}

type ItemPatch struct {
	kernel.Versioned
	ItemFields
}

type Items struct {
	res  *resource.Service[Item]
	days resource.Repo[Day]
}

func NewItems(repo resource.Repo[Item], days resource.Repo[Day], authz resource.Authorizer) *Items {
	return &Items{
		res:  resource.NewService[Item](repo, authz, resource.Config[Item]{Name: "itinerary_item", Base: ItemBase}),
		days: days,
	}
}

func (s *Items) Resource() *resource.Service[Item] { return s.res }

func (s *Items) Create(ctx context.Context, actor user.ID, tripID trip.ID, in ItemCreate) (resource.Result[Item], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(access trip.Access) (Item, error) {
		fresh := Item{Status: kernel.StatusPlanned, Category: CategoryOther}
		item, err := s.apply(ctx, fresh, in.ItemFields, access, true)
		if err != nil {
			return Item{}, err
		}
		if in.Position == nil {
			siblings, err := s.res.Repo().List(ctx, string(tripID))
			if err != nil {
				return Item{}, err
			}
			for _, other := range siblings {
				if other.DayID == item.DayID {
					item.Position++
				}
			}
		}
		return item, nil
	})
}

func (s *Items) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p ItemPatch) (resource.Result[Item], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Item]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Item, access trip.Access) (Item, error) {
		return s.apply(ctx, cur, p.ItemFields, access, false)
	})
}

func (s *Items) day(ctx context.Context, tripID, id string) (Day, error) {
	day, err := s.days.Get(ctx, tripID, id)
	if errors.Is(err, kernel.ErrNotFound) {
		var v kernel.Validator
		v.Add("dayId", "does not exist in this trip")
		return Day{}, v.Err()
	}
	return day, err
}

// apply merges f into cur and validates the result as a whole (times against the day, end
// against start), so create and update share one set of rules.
func (s *Items) apply(ctx context.Context, cur Item, f ItemFields, access trip.Access, creating bool) (Item, error) {
	var v kernel.Validator
	item := cur
	tz := access.Trip.Timezone

	if f.DayID != nil {
		item.DayID = *f.DayID
	}
	if creating {
		v.Check(f.DayID != nil && *f.DayID != "", "dayId", "is required")
		v.Check(f.Title != nil, "title", "is required")
		v.Check(f.Category != nil, "category", "is required")
	}
	if f.Title != nil {
		item.Title = v.Text("title", *f.Title, true, 200)
	}
	if f.Description != nil {
		item.Description = v.Text("description", *f.Description, false, 2000)
	}
	if f.Notes != nil {
		item.Notes = v.Text("notes", *f.Notes, false, 2000)
	}
	if f.PlaceID != nil {
		item.PlaceID = *f.PlaceID
	}
	if f.Position != nil {
		v.Check(*f.Position >= 0, "position", "must not be negative")
		item.Position = *f.Position
	}
	if f.Category != nil {
		category, err := ParseCategory(*f.Category)
		if err != nil {
			v.Add("category", err.Error())
		}
		item.Category = category
	}
	if f.Status != nil {
		status, err := kernel.ParsePlanStatus(*f.Status)
		if err != nil {
			v.Add("status", err.Error())
		}
		item.Status = status
	}
	if f.Start.Set {
		item.Start = nil
		if !f.Start.Clear {
			item.Start = v.ZonedTime("start", &f.Start.Value, tz)
		}
	}
	if f.End.Set {
		item.End = nil
		if !f.End.Clear {
			item.End = v.ZonedTime("end", &f.End.Value, tz)
		}
	}
	if f.Location.Set {
		item.Location = kernel.Location{}
		if !f.Location.Clear {
			item.Location = v.Location("location", f.Location.Value)
		}
	}
	if f.DurationMinutes.Set {
		item.DurationMinutes = nil
		if !f.DurationMinutes.Clear {
			minutes := f.DurationMinutes.Value
			v.Check(minutes >= 0 && minutes <= maxDurationMinutes, "estimatedDurationMinutes", "must be between 0 and 10080")
			item.DurationMinutes = &minutes
		}
	}
	if f.Cost.Set {
		item.Cost = nil
		if !f.Cost.Clear {
			item.Cost = v.Money("estimatedCost", &f.Cost.Value)
		}
	}
	if v.HasErrors() {
		return Item{}, v.Err()
	}

	day, err := s.day(ctx, string(access.Trip.ID), item.DayID)
	if err != nil {
		return Item{}, err
	}
	s.checkSchedule(&v, item, day)
	return item, v.Err()
}

func (s *Items) checkSchedule(v *kernel.Validator, item Item, day Day) {
	if item.End != nil && item.Start == nil {
		v.Add("end", "requires a start")
	}
	if item.Start != nil {
		v.Check(item.Start.LocalDate() == day.Date, "start.dateTime", "must fall on the day's date ("+string(day.Date)+")")
		if item.End != nil {
			v.Check(!item.End.Before(*item.Start), "end.dateTime", "must not be before the start")
		}
	}
	if item.End != nil && item.DurationMinutes != nil {
		v.Add("estimatedDurationMinutes", "must be omitted when an end time is set")
	}
}

// PlaceSnapshot is the part of a wishlist place an itinerary item is built from. The place
// domain implements PlaceReader, so the itinerary never imports it.
type PlaceSnapshot struct {
	ID              string
	Name            string
	Description     string
	Category        string
	Location        kernel.Location
	DurationMinutes *int
	Cost            *kernel.Money
	Notes           string
}

type PlaceReader interface {
	Snapshot(ctx context.Context, tripID, placeID string) (PlaceSnapshot, error)
}

type ScheduleFromPlace struct {
	ID       string                                 `json:"id"`
	PlaceID  string                                 `json:"placeId"`
	DayID    string                                 `json:"dayId"`
	Start    kernel.Optional[kernel.ZonedTimeInput] `json:"start"`
	End      kernel.Optional[kernel.ZonedTimeInput] `json:"end"`
	Position *int                                   `json:"position"`
}

// CreateFromPlace copies the place into a new item on the given day. The item keeps a link to the
// place but is independent afterwards: later edits to either do not propagate.
func (s *Items) CreateFromPlace(ctx context.Context, places PlaceReader, actor user.ID, tripID trip.ID, in ScheduleFromPlace) (resource.Result[Item], error) {
	snap, err := places.Snapshot(ctx, string(tripID), in.PlaceID)
	if err != nil {
		return resource.Result[Item]{}, err
	}
	fields := ItemFields{
		DayID: &in.DayID, Title: &snap.Name, Description: &snap.Description, Category: &snap.Category,
		Notes: &snap.Notes, PlaceID: &snap.ID, Position: in.Position, Start: in.Start, End: in.End,
	}
	if !snap.Location.IsZero() {
		fields.Location = kernel.Optional[kernel.LocationInput]{Set: true, Value: locationInput(snap.Location)}
	}
	if snap.DurationMinutes != nil && !in.End.Set {
		fields.DurationMinutes = kernel.Optional[int]{Set: true, Value: *snap.DurationMinutes}
	}
	if snap.Cost != nil {
		fields.Cost = kernel.Optional[kernel.MoneyInput]{Set: true, Value: kernel.MoneyInput{Amount: snap.Cost.Amount, Currency: string(snap.Cost.Currency)}}
	}
	return s.Create(ctx, actor, tripID, ItemCreate{ID: in.ID, ItemFields: fields})
}

func locationInput(l kernel.Location) kernel.LocationInput {
	in := kernel.LocationInput{Name: l.Name, Address: l.Address}
	if l.Coordinates != nil {
		lat, lng := l.Coordinates.Lat, l.Coordinates.Lng
		in.Latitude, in.Longitude = &lat, &lng
	}
	return in
}
