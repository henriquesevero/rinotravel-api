// Package place holds the trip's wishlist: places worth visiting and restaurants to try, before
// they are given a day and time in the itinerary.
package place

import (
	"context"
	"errors"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/itinerary"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type Category string

const (
	CategoryAttraction Category = "ATTRACTION"
	CategoryRestaurant Category = "RESTAURANT"
	CategoryShopping   Category = "SHOPPING"
	CategoryOther      Category = "OTHER"
)

func ParseCategory(s string) (Category, error) {
	switch c := Category(s); c {
	case CategoryAttraction, CategoryRestaurant, CategoryShopping, CategoryOther:
		return c, nil
	}
	return "", errors.New("must be one of ATTRACTION, RESTAURANT, SHOPPING, OTHER")
}

type Priority string

const (
	PriorityHigh   Priority = "HIGH"
	PriorityMedium Priority = "MEDIUM"
	PriorityLow    Priority = "LOW"
)

func ParsePriority(s string) (Priority, error) {
	switch p := Priority(s); p {
	case PriorityHigh, PriorityMedium, PriorityLow:
		return p, nil
	}
	return "", errors.New("must be one of HIGH, MEDIUM, LOW")
}

const maxDurationMinutes = 7 * 24 * 60

type Place struct {
	kernel.Base
	Name            string
	Description     string
	Category        Category
	Location        kernel.Location
	DurationMinutes *int
	Cost            *kernel.Money
	Priority        Priority
	Notes           string
	// ExternalID is the provider's id (for example a Google place id) for later enrichment.
	ExternalID string
}

func PlaceBase(p *Place) *kernel.Base { return &p.Base }

type PlaceFields struct {
	Name            *string                               `json:"name"`
	Description     *string                               `json:"description"`
	Category        *string                               `json:"category"`
	Priority        *string                               `json:"priority"`
	Notes           *string                               `json:"notes"`
	ExternalID      *string                               `json:"externalId"`
	Location        kernel.Optional[kernel.LocationInput] `json:"location"`
	DurationMinutes kernel.Optional[int]                  `json:"estimatedDurationMinutes"`
	Cost            kernel.Optional[kernel.MoneyInput]    `json:"estimatedCost"`
}

type PlaceCreate struct {
	ID string `json:"id"`
	PlaceFields
}

type PlacePatch struct {
	kernel.Versioned
	PlaceFields
}

type Places struct {
	res *resource.Service[Place]
}

func NewPlaces(repo resource.Repo[Place], authz resource.Authorizer) *Places {
	return &Places{res: resource.NewService[Place](repo, authz, resource.Config[Place]{Name: "place", Base: PlaceBase})}
}

func (s *Places) Resource() *resource.Service[Place] { return s.res }

func (s *Places) Create(ctx context.Context, actor user.ID, tripID trip.ID, in PlaceCreate) (resource.Result[Place], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(trip.Access) (Place, error) {
		return applyPlace(Place{Priority: PriorityMedium, Category: CategoryOther}, in.PlaceFields, true)
	})
}

func (s *Places) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p PlacePatch) (resource.Result[Place], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Place]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Place, _ trip.Access) (Place, error) {
		return applyPlace(cur, p.PlaceFields, false)
	})
}

func applyPlace(cur Place, f PlaceFields, creating bool) (Place, error) {
	var v kernel.Validator
	p := cur
	if creating {
		v.Check(f.Name != nil, "name", "is required")
		v.Check(f.Category != nil, "category", "is required")
	}
	if f.Name != nil {
		p.Name = v.Text("name", *f.Name, true, 200)
	}
	if f.Description != nil {
		p.Description = v.Text("description", *f.Description, false, 2000)
	}
	if f.Notes != nil {
		p.Notes = v.Text("notes", *f.Notes, false, 2000)
	}
	if f.ExternalID != nil {
		p.ExternalID = v.Text("externalId", *f.ExternalID, false, 300)
	}
	if f.Category != nil {
		category, err := ParseCategory(*f.Category)
		if err != nil {
			v.Add("category", err.Error())
		}
		p.Category = category
	}
	if f.Priority != nil {
		priority, err := ParsePriority(*f.Priority)
		if err != nil {
			v.Add("priority", err.Error())
		}
		p.Priority = priority
	}
	if f.Location.Set {
		p.Location = kernel.Location{}
		if !f.Location.Clear {
			p.Location = v.Location("location", f.Location.Value)
		}
	}
	if f.DurationMinutes.Set {
		p.DurationMinutes = nil
		if !f.DurationMinutes.Clear {
			minutes := f.DurationMinutes.Value
			v.Check(minutes >= 0 && minutes <= maxDurationMinutes, "estimatedDurationMinutes", "must be between 0 and 10080")
			p.DurationMinutes = &minutes
		}
	}
	if f.Cost.Set {
		p.Cost = nil
		if !f.Cost.Clear {
			p.Cost = v.Money("estimatedCost", &f.Cost.Value)
		}
	}
	return p, v.Err()
}

// Snapshot implements itinerary.PlaceReader.
func (s *Places) Snapshot(ctx context.Context, tripID, placeID string) (itinerary.PlaceSnapshot, error) {
	p, err := s.res.Repo().Get(ctx, tripID, placeID)
	if errors.Is(err, kernel.ErrNotFound) {
		return itinerary.PlaceSnapshot{}, apperror.NotFound("place_not_found", "The requested resource does not exist.")
	}
	if err != nil {
		return itinerary.PlaceSnapshot{}, err
	}
	return itinerary.PlaceSnapshot{
		ID: p.ID, Name: p.Name, Description: p.Description, Category: string(p.Category),
		Location: p.Location, DurationMinutes: p.DurationMinutes, Cost: p.Cost, Notes: p.Notes,
	}, nil
}
