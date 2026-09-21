package booking

import (
	"context"
	"errors"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type TicketKind string

const (
	TicketAttraction TicketKind = "ATTRACTION"
	TicketShow       TicketKind = "SHOW"
	TicketMuseum     TicketKind = "MUSEUM"
	TicketSport      TicketKind = "SPORT"
	TicketTour       TicketKind = "TOUR"
	TicketTransport  TicketKind = "TRANSPORT"
	TicketOther      TicketKind = "OTHER"
)

func ParseTicketKind(s string) (TicketKind, error) {
	switch k := TicketKind(s); k {
	case TicketAttraction, TicketShow, TicketMuseum, TicketSport, TicketTour, TicketTransport, TicketOther:
		return k, nil
	}
	return "", errors.New("must be one of ATTRACTION, SHOW, MUSEUM, SPORT, TOUR, TRANSPORT, OTHER")
}

const maxTicketQuantity = 999

// Ticket is an entry ticket for something to do (a show, a museum, a tour): what it is, where and
// when it is used, and optionally the file itself, which lives in the trip's documents.
type Ticket struct {
	kernel.Base
	Name string
	Kind TicketKind
	// Location is where the ticket is used; it puts the visit on the day's map.
	Location kernel.Location
	Start    *kernel.ZonedTime
	End      *kernel.ZonedTime
	Quantity int
	// ConfirmationCode is sensitive and hidden from viewers.
	ConfirmationCode string
	// Seat is whatever tells the seat apart: section, row, gate, time slot.
	Seat   string
	Cost   *kernel.Money
	Status kernel.PlanStatus
	// DocumentID ties the ticket to a file in the trip's documents. The file stays a document, so it
	// is listed there too; deleting it leaves the ticket without a file.
	DocumentID string
	Notes      string
}

func TicketBase(t *Ticket) *kernel.Base { return &t.Base }

// Entries puts the ticket on the timeline of the day it is used, when it has a time.
func (t Ticket) Entries() []kernel.TimelineEntry {
	if t.Start == nil {
		return nil
	}
	start := *t.Start
	return []kernel.TimelineEntry{{
		Kind: "ticket", ID: t.ID, Title: t.Name, Subtitle: string(t.Kind), Status: string(t.Status),
		Start: &start, End: t.End, Day: start.LocalDate(),
	}}
}

type TicketFields struct {
	Name             *string                                `json:"name"`
	Kind             *string                                `json:"kind"`
	Status           *string                                `json:"status"`
	Location         kernel.Optional[kernel.LocationInput]  `json:"location"`
	Start            kernel.Optional[kernel.ZonedTimeInput] `json:"start"`
	End              kernel.Optional[kernel.ZonedTimeInput] `json:"end"`
	Quantity         *int                                   `json:"quantity"`
	ConfirmationCode *string                                `json:"confirmationCode"`
	Seat             *string                                `json:"seat"`
	Cost             kernel.Optional[kernel.MoneyInput]     `json:"cost"`
	DocumentID       kernel.Optional[string]                `json:"documentId"`
	Notes            *string                                `json:"notes"`
}

type TicketCreate struct {
	ID string `json:"id"`
	TicketFields
}

type TicketPatch struct {
	kernel.Versioned
	TicketFields
}

// DocumentReader is the port to the trip's documents: whether the actor can see one, which is what
// it takes to attach it to a ticket.
type DocumentReader interface {
	Readable(ctx context.Context, actor user.ID, tripID trip.ID, documentID string) (bool, error)
}

type Tickets struct {
	res  *resource.Service[Ticket]
	docs DocumentReader
}

func NewTickets(repo resource.Repo[Ticket], authz resource.Authorizer, docs DocumentReader) *Tickets {
	return &Tickets{
		res:  resource.NewService[Ticket](repo, authz, resource.Config[Ticket]{Name: "ticket", Base: TicketBase}),
		docs: docs,
	}
}

func (s *Tickets) Resource() *resource.Service[Ticket] { return s.res }

func (s *Tickets) Create(ctx context.Context, actor user.ID, tripID trip.ID, in TicketCreate) (resource.Result[Ticket], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(a trip.Access) (Ticket, error) {
		fresh := Ticket{Kind: TicketAttraction, Quantity: 1, Status: kernel.StatusPlanned}
		return s.apply(ctx, actor, fresh, in.TicketFields, a, true)
	})
}

func (s *Tickets) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p TicketPatch) (resource.Result[Ticket], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Ticket]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Ticket, a trip.Access) (Ticket, error) {
		return s.apply(ctx, actor, cur, p.TicketFields, a, false)
	})
}

func (s *Tickets) apply(ctx context.Context, actor user.ID, cur Ticket, f TicketFields, access trip.Access, creating bool) (Ticket, error) {
	var v kernel.Validator
	t := cur
	tz := access.Trip.Timezone
	if creating {
		v.Check(f.Name != nil, "name", "is required")
	}
	if f.Name != nil {
		t.Name = v.Text("name", *f.Name, true, 200)
	}
	if f.Kind != nil {
		kind, err := ParseTicketKind(*f.Kind)
		if err != nil {
			v.Add("kind", err.Error())
		}
		t.Kind = kind
	}
	if f.Status != nil {
		status, err := kernel.ParsePlanStatus(*f.Status)
		if err != nil {
			v.Add("status", err.Error())
		}
		t.Status = status
	}
	if f.Location.Set {
		t.Location = kernel.Location{}
		if !f.Location.Clear {
			t.Location = v.Location("location", f.Location.Value)
		}
	}
	if f.Start.Set {
		t.Start = nil
		if !f.Start.Clear {
			t.Start = v.ZonedTime("start", &f.Start.Value, tz)
		}
	}
	if f.End.Set {
		t.End = nil
		if !f.End.Clear {
			t.End = v.ZonedTime("end", &f.End.Value, tz)
		}
	}
	if f.Quantity != nil {
		v.Check(*f.Quantity >= 1 && *f.Quantity <= maxTicketQuantity, "quantity", "must be between 1 and 999")
		t.Quantity = *f.Quantity
	}
	if f.ConfirmationCode != nil {
		t.ConfirmationCode = v.Text("confirmationCode", *f.ConfirmationCode, false, 100)
	}
	if f.Seat != nil {
		t.Seat = v.Text("seat", *f.Seat, false, 100)
	}
	if f.Cost.Set {
		t.Cost = nil
		if !f.Cost.Clear {
			t.Cost = v.Money("cost", &f.Cost.Value)
		}
	}
	if f.Notes != nil {
		t.Notes = v.Text("notes", *f.Notes, false, 2000)
	}
	if f.DocumentID.Set {
		t.DocumentID = ""
		if !f.DocumentID.Clear {
			t.DocumentID = f.DocumentID.Value
			if !ids.IsValid(t.DocumentID) {
				v.Add("documentId", "must be a lowercase UUID")
			}
		}
	}
	if t.End != nil && t.Start == nil {
		v.Add("end", "requires a start")
	}
	if t.Start != nil && t.End != nil {
		v.Check(!t.End.Before(*t.Start), "end.dateTime", "must not be before the start")
	}
	if v.HasErrors() {
		return Ticket{}, v.Err()
	}
	// Only a file that is new to the ticket is checked, so an old ticket whose file was since deleted
	// can still be edited.
	if t.DocumentID != "" && t.DocumentID != cur.DocumentID {
		if s.docs == nil {
			v.Add("documentId", "documents are not available on this server")
			return Ticket{}, v.Err()
		}
		ok, err := s.docs.Readable(ctx, actor, access.Trip.ID, t.DocumentID)
		if err != nil {
			return Ticket{}, err
		}
		if !ok {
			v.Add("documentId", "does not exist in this trip's documents")
			return Ticket{}, v.Err()
		}
	}
	return t, nil
}
