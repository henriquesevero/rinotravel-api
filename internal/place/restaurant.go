package place

import (
	"context"
	"errors"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type RestaurantStatus string

const (
	StatusWishlist RestaurantStatus = "WISHLIST"
	StatusPlanned  RestaurantStatus = "PLANNED"
	StatusReserved RestaurantStatus = "RESERVED"
	StatusVisited  RestaurantStatus = "VISITED"
)

func ParseRestaurantStatus(s string) (RestaurantStatus, error) {
	switch st := RestaurantStatus(s); st {
	case StatusWishlist, StatusPlanned, StatusReserved, StatusVisited:
		return st, nil
	}
	return "", errors.New("must be one of WISHLIST, PLANNED, RESERVED, VISITED")
}

const (
	maxDishes    = 20
	maxDishChars = 100
)

type Restaurant struct {
	kernel.Base
	Name     string
	Cuisine  string
	Location kernel.Location
	Cost     *kernel.Money
	// ReservationAt is when the table is booked; it puts the restaurant on the itinerary timeline.
	ReservationAt *kernel.ZonedTime
	// ReservationCode is sensitive and hidden from viewers.
	ReservationCode string
	DesiredDishes   []string
	Notes           string
	Status          RestaurantStatus
	ExternalID      string
}

func RestaurantBase(r *Restaurant) *kernel.Base { return &r.Base }

func (r Restaurant) Entry() (kernel.TimelineEntry, bool) {
	if r.ReservationAt == nil {
		return kernel.TimelineEntry{}, false
	}
	return kernel.TimelineEntry{
		Kind: "restaurant_reservation", ID: r.ID, Title: r.Name, Subtitle: r.Cuisine, Status: string(r.Status),
		Start: r.ReservationAt, Day: r.ReservationAt.LocalDate(),
	}, true
}

type RestaurantFields struct {
	Name            *string                                `json:"name"`
	Cuisine         *string                                `json:"cuisine"`
	Notes           *string                                `json:"notes"`
	Status          *string                                `json:"status"`
	ExternalID      *string                                `json:"externalId"`
	ReservationCode *string                                `json:"reservationCode"`
	DesiredDishes   *[]string                              `json:"desiredDishes"`
	Location        kernel.Optional[kernel.LocationInput]  `json:"location"`
	Cost            kernel.Optional[kernel.MoneyInput]     `json:"estimatedCost"`
	ReservationAt   kernel.Optional[kernel.ZonedTimeInput] `json:"reservationAt"`
}

type RestaurantCreate struct {
	ID string `json:"id"`
	RestaurantFields
}

type RestaurantPatch struct {
	kernel.Versioned
	RestaurantFields
}

type Restaurants struct {
	res *resource.Service[Restaurant]
}

func NewRestaurants(repo resource.Repo[Restaurant], authz resource.Authorizer) *Restaurants {
	return &Restaurants{res: resource.NewService[Restaurant](repo, authz, resource.Config[Restaurant]{Name: "restaurant", Base: RestaurantBase})}
}

func (s *Restaurants) Resource() *resource.Service[Restaurant] { return s.res }

func (s *Restaurants) Create(ctx context.Context, actor user.ID, tripID trip.ID, in RestaurantCreate) (resource.Result[Restaurant], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(a trip.Access) (Restaurant, error) {
		return applyRestaurant(Restaurant{Status: StatusWishlist}, in.RestaurantFields, a, true)
	})
}

func (s *Restaurants) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p RestaurantPatch) (resource.Result[Restaurant], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Restaurant]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Restaurant, a trip.Access) (Restaurant, error) {
		return applyRestaurant(cur, p.RestaurantFields, a, false)
	})
}

func applyRestaurant(cur Restaurant, f RestaurantFields, access trip.Access, creating bool) (Restaurant, error) {
	var v kernel.Validator
	r := cur
	if creating {
		v.Check(f.Name != nil, "name", "is required")
	}
	if f.Name != nil {
		r.Name = v.Text("name", *f.Name, true, 200)
	}
	if f.Cuisine != nil {
		r.Cuisine = v.Text("cuisine", *f.Cuisine, false, 100)
	}
	if f.Notes != nil {
		r.Notes = v.Text("notes", *f.Notes, false, 2000)
	}
	if f.ExternalID != nil {
		r.ExternalID = v.Text("externalId", *f.ExternalID, false, 300)
	}
	if f.ReservationCode != nil {
		r.ReservationCode = v.Text("reservationCode", *f.ReservationCode, false, 50)
	}
	if f.Status != nil {
		status, err := ParseRestaurantStatus(*f.Status)
		if err != nil {
			v.Add("status", err.Error())
		}
		r.Status = status
	}
	if f.DesiredDishes != nil {
		v.Check(len(*f.DesiredDishes) <= maxDishes, "desiredDishes", "must have at most 20 dishes")
		dishes := make([]string, 0, len(*f.DesiredDishes))
		for _, dish := range *f.DesiredDishes {
			if d := v.Text("desiredDishes", dish, true, maxDishChars); d != "" {
				dishes = append(dishes, d)
			}
		}
		r.DesiredDishes = dishes
	}
	if f.Location.Set {
		r.Location = kernel.Location{}
		if !f.Location.Clear {
			r.Location = v.Location("location", f.Location.Value)
		}
	}
	if f.Cost.Set {
		r.Cost = nil
		if !f.Cost.Clear {
			r.Cost = v.Money("estimatedCost", &f.Cost.Value)
		}
	}
	if f.ReservationAt.Set {
		r.ReservationAt = nil
		if !f.ReservationAt.Clear {
			r.ReservationAt = v.ZonedTime("reservationAt", &f.ReservationAt.Value, access.Trip.Timezone)
		}
	}
	if !v.HasErrors() {
		v.Check(r.Status != StatusReserved || r.ReservationAt != nil, "reservationAt", "is required when the status is RESERVED")
	}
	return r, v.Err()
}

// TimelineSource contributes reservations to the itinerary timeline.
type TimelineSource struct {
	repo resource.Repo[Restaurant]
}

func NewTimelineSource(repo resource.Repo[Restaurant]) TimelineSource {
	return TimelineSource{repo: repo}
}

func (t TimelineSource) Entries(ctx context.Context, tripID string) ([]kernel.TimelineEntry, error) {
	restaurants, err := t.repo.List(ctx, tripID)
	if err != nil {
		return nil, err
	}
	var out []kernel.TimelineEntry
	for _, r := range restaurants {
		if entry, ok := r.Entry(); ok {
			out = append(out, entry)
		}
	}
	return out, nil
}
