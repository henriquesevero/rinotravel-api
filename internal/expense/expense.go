// Package expense is the trip's money: what was spent, what is still meant to be bought, and the
// budget both are measured against. An expense is one line (a meal, a souvenir, a jacket) that can
// be tied to a place of the trip, so a shopping list can hang off the shop it will be bought in.
package expense

import (
	"context"
	"errors"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type Category string

const (
	Food        Category = "FOOD"
	Lodging     Category = "LODGING"
	Transport   Category = "TRANSPORT"
	Activities  Category = "ACTIVITIES"
	Souvenirs   Category = "SOUVENIRS"
	Clothes     Category = "CLOTHES"
	Electronics Category = "ELECTRONICS"
	Other       Category = "OTHER"
)

func ParseCategory(s string) (Category, error) {
	switch c := Category(s); c {
	case Food, Lodging, Transport, Activities, Souvenirs, Clothes, Electronics, Other:
		return c, nil
	}
	return "", errors.New("must be one of FOOD, LODGING, TRANSPORT, ACTIVITIES, SOUVENIRS, CLOTHES, ELECTRONICS, OTHER")
}

type Status string

const (
	// Planned is something meant to be bought or paid: it counts as forecast, at its estimate.
	Planned Status = "PLANNED"
	// Paid is money already spent, at its actual amount.
	Paid Status = "PAID"
)

func ParseStatus(s string) (Status, error) {
	switch st := Status(s); st {
	case Planned, Paid:
		return st, nil
	}
	return "", errors.New("must be PLANNED or PAID")
}

// linkTypes are the records of the trip an expense can be tied to.
var linkTypes = map[string]bool{
	"place": true, "restaurant": true, "itinerary_item": true, "ticket": true, "hotel": true, "flight": true,
}

type Expense struct {
	kernel.Base
	Name     string
	Category Category
	Status   Status
	// Estimate is what it is expected to cost; Actual is what was paid. A planned expense has only
	// the estimate, a paid one has the actual (and may keep the estimate to compare against).
	Estimate *kernel.Money
	Actual   *kernel.Money
	// Date is the day it was or will be paid, when it matters.
	Date kernel.Date
	// LinkType and LinkID tie it to a place, restaurant, item or booking of the trip.
	LinkType string
	LinkID   string
	Notes    string
}

func Base(e *Expense) *kernel.Base { return &e.Base }

type LinkInput struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Fields struct {
	Name     *string                            `json:"name"`
	Category *string                            `json:"category"`
	Status   *string                            `json:"status"`
	Estimate kernel.Optional[kernel.MoneyInput] `json:"estimate"`
	Actual   kernel.Optional[kernel.MoneyInput] `json:"actual"`
	Date     kernel.Optional[string]            `json:"date"`
	Link     kernel.Optional[LinkInput]         `json:"link"`
	Notes    *string                            `json:"notes"`
}

type Create struct {
	ID string `json:"id"`
	Fields
}

type Patch struct {
	kernel.Versioned
	Fields
}

type Expenses struct {
	res *resource.Service[Expense]
}

func NewExpenses(repo resource.Repo[Expense], authz resource.Authorizer) *Expenses {
	return &Expenses{res: resource.NewService[Expense](repo, authz, resource.Config[Expense]{Name: "expense", Base: Base})}
}

func (s *Expenses) Resource() *resource.Service[Expense] { return s.res }

func (s *Expenses) Create(ctx context.Context, actor user.ID, tripID trip.ID, in Create) (resource.Result[Expense], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(trip.Access) (Expense, error) {
		return apply(Expense{Category: Other, Status: Planned}, in.Fields, true)
	})
}

func (s *Expenses) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p Patch) (resource.Result[Expense], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Expense]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Expense, _ trip.Access) (Expense, error) {
		return apply(cur, p.Fields, false)
	})
}

func apply(cur Expense, f Fields, creating bool) (Expense, error) {
	var v kernel.Validator
	e := cur
	if creating {
		v.Check(f.Name != nil, "name", "is required")
	}
	if f.Name != nil {
		e.Name = v.Text("name", *f.Name, true, 200)
	}
	if f.Category != nil {
		category, err := ParseCategory(*f.Category)
		if err != nil {
			v.Add("category", err.Error())
		}
		e.Category = category
	}
	if f.Status != nil {
		status, err := ParseStatus(*f.Status)
		if err != nil {
			v.Add("status", err.Error())
		}
		e.Status = status
	}
	if f.Estimate.Set {
		e.Estimate = nil
		if !f.Estimate.Clear {
			e.Estimate = v.Money("estimate", &f.Estimate.Value)
		}
	}
	if f.Actual.Set {
		e.Actual = nil
		if !f.Actual.Clear {
			e.Actual = v.Money("actual", &f.Actual.Value)
		}
	}
	if f.Date.Set {
		e.Date = ""
		if !f.Date.Clear {
			date, err := kernel.ParseDate(f.Date.Value)
			if err != nil {
				v.Add("date", err.Error())
			}
			e.Date = date
		}
	}
	if f.Link.Set {
		e.LinkType, e.LinkID = "", ""
		if !f.Link.Clear {
			v.Check(linkTypes[f.Link.Value.Type], "link.type", "must be one of place, restaurant, itinerary_item, ticket, hotel, flight")
			v.Check(ids.IsValid(f.Link.Value.ID), "link.id", "must be a lowercase UUID")
			e.LinkType, e.LinkID = f.Link.Value.Type, f.Link.Value.ID
		}
	}
	if f.Notes != nil {
		e.Notes = v.Text("notes", *f.Notes, false, 2000)
	}
	if !v.HasErrors() {
		v.Check(e.Estimate != nil || e.Actual != nil, "estimate", "an estimate or the amount paid is required")
		switch e.Status {
		case Paid:
			v.Check(e.Actual != nil, "actual", "is required once it is paid")
		case Planned:
			v.Check(e.Actual == nil, "actual", "must be empty while it is only planned")
		}
		if e.Estimate != nil && e.Actual != nil {
			v.Check(e.Estimate.Currency == e.Actual.Currency, "actual.currency", "must be the same currency as the estimate")
		}
	}
	return e, v.Err()
}

// TotalCategory is the budget line for the whole trip; the others are limits for one category.
const TotalCategory = "TOTAL"

// Limit is the most the trip, or one category of it, is meant to cost.
type Limit struct {
	kernel.Base
	// Category is TOTAL or an expense category. There is one limit per category in a trip.
	Category string
	Amount   kernel.Money
}

func LimitBase(l *Limit) *kernel.Base { return &l.Base }

func parseLimitCategory(s string) (string, error) {
	if s == TotalCategory {
		return s, nil
	}
	if c, err := ParseCategory(s); err == nil {
		return string(c), nil
	}
	return "", errors.New("must be TOTAL or an expense category")
}

type LimitCreate struct {
	ID       string            `json:"id"`
	Category string            `json:"category"`
	Amount   kernel.MoneyInput `json:"amount"`
}

type LimitPatch struct {
	kernel.Versioned
	Amount *kernel.MoneyInput `json:"amount"`
}

type Limits struct {
	res *resource.Service[Limit]
}

func NewLimits(repo resource.Repo[Limit], authz resource.Authorizer) *Limits {
	return &Limits{res: resource.NewService[Limit](repo, authz, resource.Config[Limit]{Name: "budget_limit", Base: LimitBase})}
}

func (s *Limits) Resource() *resource.Service[Limit] { return s.res }

func (s *Limits) Create(ctx context.Context, actor user.ID, tripID trip.ID, in LimitCreate) (resource.Result[Limit], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(trip.Access) (Limit, error) {
		var v kernel.Validator
		category, err := parseLimitCategory(in.Category)
		if err != nil {
			v.Add("category", err.Error())
		}
		amount := v.Money("amount", &in.Amount)
		if err := v.Err(); err != nil {
			return Limit{}, err
		}
		existing, err := s.res.Repo().List(ctx, string(tripID))
		if err != nil {
			return Limit{}, err
		}
		for _, other := range existing {
			if other.Category == category {
				return Limit{}, apperror.Conflict("budget_exists", "This trip already has a budget for that category.")
			}
		}
		return Limit{Category: category, Amount: *amount}, nil
	})
}

func (s *Limits) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p LimitPatch) (resource.Result[Limit], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Limit]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Limit, _ trip.Access) (Limit, error) {
		var v kernel.Validator
		if p.Amount != nil {
			if amount := v.Money("amount", p.Amount); amount != nil {
				cur.Amount = *amount
			}
		}
		return cur, v.Err()
	})
}
