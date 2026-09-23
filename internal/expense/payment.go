package expense

import (
	"context"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

// Payment says whether the price a record carries (a ticket, a flight, a meal of the itinerary) has
// been paid. Those prices live on the records themselves and are not copied into the expenses, so
// whether they were paid has to be kept somewhere else: here. Without a payment, a price counts as
// paid once its date has passed; a payment overrides that either way.
type Payment struct {
	kernel.Base
	LinkType string
	LinkID   string
	Paid     bool
}

func PaymentBase(p *Payment) *kernel.Base { return &p.Base }

type PaymentCreate struct {
	ID   string    `json:"id"`
	Link LinkInput `json:"link"`
	Paid *bool     `json:"paid"`
}

type PaymentPatch struct {
	kernel.Versioned
	Paid *bool `json:"paid"`
}

type Payments struct {
	res *resource.Service[Payment]
}

func NewPayments(repo resource.Repo[Payment], authz resource.Authorizer) *Payments {
	return &Payments{res: resource.NewService[Payment](repo, authz, resource.Config[Payment]{Name: "payment", Base: PaymentBase})}
}

func (s *Payments) Resource() *resource.Service[Payment] { return s.res }

func (s *Payments) Create(ctx context.Context, actor user.ID, tripID trip.ID, in PaymentCreate) (resource.Result[Payment], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(trip.Access) (Payment, error) {
		var v kernel.Validator
		v.Check(linkTypes[in.Link.Type], "link.type", "must be one of place, restaurant, itinerary_item, ticket, hotel, flight")
		v.Check(ids.IsValid(in.Link.ID), "link.id", "must be a lowercase UUID")
		if err := v.Err(); err != nil {
			return Payment{}, err
		}
		existing, err := s.res.Repo().List(ctx, string(tripID))
		if err != nil {
			return Payment{}, err
		}
		for _, other := range existing {
			if other.LinkType == in.Link.Type && other.LinkID == in.Link.ID {
				return Payment{}, apperror.Conflict("payment_exists", "That price already has a payment mark.")
			}
		}
		paid := true
		if in.Paid != nil {
			paid = *in.Paid
		}
		return Payment{LinkType: in.Link.Type, LinkID: in.Link.ID, Paid: paid}, nil
	})
}

func (s *Payments) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p PaymentPatch) (resource.Result[Payment], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Payment]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Payment, _ trip.Access) (Payment, error) {
		if p.Paid != nil {
			cur.Paid = *p.Paid
		}
		return cur, nil
	})
}
