package booking

import (
	"context"
	"regexp"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

var phonePattern = regexp.MustCompile(`^[0-9+()\-. ]{5,30}$`)

type Hotel struct {
	kernel.Base
	Name     string
	Location kernel.Location
	CheckIn  kernel.ZonedTime
	CheckOut kernel.ZonedTime
	// ConfirmationCode is sensitive and hidden from viewers.
	ConfirmationCode string
	ContactPhone     string
	BookingURL       string
	Notes            string
}

func HotelBase(h *Hotel) *kernel.Base { return &h.Base }

func (h Hotel) Entries() []kernel.TimelineEntry {
	in, out := h.CheckIn, h.CheckOut
	return []kernel.TimelineEntry{
		{Kind: "hotel_check_in", ID: h.ID, Title: h.Name, Subtitle: "check-in", Start: &in, Day: in.LocalDate()},
		{Kind: "hotel_check_out", ID: h.ID, Title: h.Name, Subtitle: "check-out", Start: &out, Day: out.LocalDate()},
	}
}

type HotelFields struct {
	Name             *string                               `json:"name"`
	Location         kernel.Optional[kernel.LocationInput] `json:"location"`
	CheckIn          *kernel.ZonedTimeInput                `json:"checkIn"`
	CheckOut         *kernel.ZonedTimeInput                `json:"checkOut"`
	ConfirmationCode *string                               `json:"confirmationCode"`
	ContactPhone     *string                               `json:"contactPhone"`
	BookingURL       *string                               `json:"bookingUrl"`
	Notes            *string                               `json:"notes"`
}

type HotelCreate struct {
	ID string `json:"id"`
	HotelFields
}

type HotelPatch struct {
	kernel.Versioned
	HotelFields
}

type Hotels struct {
	res *resource.Service[Hotel]
}

func NewHotels(repo resource.Repo[Hotel], authz resource.Authorizer) *Hotels {
	return &Hotels{res: resource.NewService[Hotel](repo, authz, resource.Config[Hotel]{Name: "hotel", Base: HotelBase})}
}

func (s *Hotels) Resource() *resource.Service[Hotel] { return s.res }

func (s *Hotels) Create(ctx context.Context, actor user.ID, tripID trip.ID, in HotelCreate) (resource.Result[Hotel], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(a trip.Access) (Hotel, error) {
		return applyHotel(Hotel{}, in.HotelFields, a, true)
	})
}

func (s *Hotels) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p HotelPatch) (resource.Result[Hotel], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Hotel]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Hotel, a trip.Access) (Hotel, error) {
		return applyHotel(cur, p.HotelFields, a, false)
	})
}

func applyHotel(cur Hotel, f HotelFields, access trip.Access, creating bool) (Hotel, error) {
	var v kernel.Validator
	h := cur
	tz := access.Trip.Timezone
	if creating {
		v.Check(f.Name != nil, "name", "is required")
		v.Check(f.CheckIn != nil, "checkIn", "is required")
		v.Check(f.CheckOut != nil, "checkOut", "is required")
	}
	if f.Name != nil {
		h.Name = v.Text("name", *f.Name, true, 200)
	}
	if f.Location.Set {
		h.Location = kernel.Location{}
		if !f.Location.Clear {
			h.Location = v.Location("location", f.Location.Value)
		}
	}
	if parsed := v.ZonedTime("checkIn", f.CheckIn, tz); parsed != nil {
		h.CheckIn = *parsed
	}
	if parsed := v.ZonedTime("checkOut", f.CheckOut, tz); parsed != nil {
		h.CheckOut = *parsed
	}
	if f.ConfirmationCode != nil {
		h.ConfirmationCode = v.Text("confirmationCode", *f.ConfirmationCode, false, 50)
	}
	if f.ContactPhone != nil {
		phone := v.Text("contactPhone", *f.ContactPhone, false, 30)
		v.Check(phone == "" || phonePattern.MatchString(phone), "contactPhone", "must be a phone number")
		h.ContactPhone = phone
	}
	if f.BookingURL != nil {
		h.BookingURL = v.HTTPURL("bookingUrl", *f.BookingURL)
	}
	if f.Notes != nil {
		h.Notes = v.Text("notes", *f.Notes, false, 2000)
	}
	if !v.HasErrors() {
		v.Check(h.CheckOut.Instant.After(h.CheckIn.Instant), "checkOut.dateTime", "must be after check-in")
	}
	return h, v.Err()
}

// TimelineSource adds flights, hotel stays and tickets to the itinerary timeline.
type TimelineSource struct {
	flights resource.Repo[Flight]
	hotels  resource.Repo[Hotel]
	tickets resource.Repo[Ticket]
}

func NewTimelineSource(flights resource.Repo[Flight], hotels resource.Repo[Hotel], tickets resource.Repo[Ticket]) TimelineSource {
	return TimelineSource{flights: flights, hotels: hotels, tickets: tickets}
}

func (t TimelineSource) Entries(ctx context.Context, tripID string) ([]kernel.TimelineEntry, error) {
	var out []kernel.TimelineEntry
	flights, err := t.flights.List(ctx, tripID)
	if err != nil {
		return nil, err
	}
	for _, f := range flights {
		out = append(out, f.Entries()...)
	}
	hotels, err := t.hotels.List(ctx, tripID)
	if err != nil {
		return nil, err
	}
	for _, h := range hotels {
		out = append(out, h.Entries()...)
	}
	tickets, err := t.tickets.List(ctx, tripID)
	if err != nil {
		return nil, err
	}
	for _, k := range tickets {
		out = append(out, k.Entries()...)
	}
	return out, nil
}
