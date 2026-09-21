package httpapi

import (
	"log/slog"
	"net/http"

	authapi "rinotravel-api/internal/auth/httpapi"
	"rinotravel-api/internal/booking"
	"rinotravel-api/internal/resource/httpres"
	"rinotravel-api/internal/syncengine"
	"rinotravel-api/internal/trip"
)

type Deps struct {
	Logger  *slog.Logger
	Guard   authapi.Guard
	Flights *booking.Flights
	Hotels  *booking.Hotels
	Tickets *booking.Tickets
}

type Handler struct {
	Flights httpres.Routes[booking.Flight, booking.FlightCreate, booking.FlightPatch]
	Hotels  httpres.Routes[booking.Hotel, booking.HotelCreate, booking.HotelPatch]
	Tickets httpres.Routes[booking.Ticket, booking.TicketCreate, booking.TicketPatch]
}

func New(d Deps) *Handler {
	return &Handler{
		Flights: httpres.Routes[booking.Flight, booking.FlightCreate, booking.FlightPatch]{
			Logger: d.Logger, Guard: d.Guard, Path: "/api/v1/trips/{tripId}/flights",
			Service: d.Flights.Resource(), Create: d.Flights.Create, Update: d.Flights.Update,
			Present: func(f booking.Flight, role trip.Role) any { return presentFlight(f, role) },
		},
		Hotels: httpres.Routes[booking.Hotel, booking.HotelCreate, booking.HotelPatch]{
			Logger: d.Logger, Guard: d.Guard, Path: "/api/v1/trips/{tripId}/hotels",
			Service: d.Hotels.Resource(), Create: d.Hotels.Create, Update: d.Hotels.Update,
			Present: func(h booking.Hotel, role trip.Role) any { return presentHotel(h, role) },
		},
		Tickets: httpres.Routes[booking.Ticket, booking.TicketCreate, booking.TicketPatch]{
			Logger: d.Logger, Guard: d.Guard, Path: "/api/v1/trips/{tripId}/tickets",
			Service: d.Tickets.Resource(), Create: d.Tickets.Create, Update: d.Tickets.Update,
			Present: func(t booking.Ticket, role trip.Role) any { return presentTicket(t, role) },
		},
	}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	h.Flights.Mount(mux)
	h.Hotels.Mount(mux)
	h.Tickets.Mount(mux)
}

func (h *Handler) SyncSources() []syncengine.Source {
	return []syncengine.Source{h.Flights.SyncSource("flight"), h.Hotels.SyncSource("hotel"), h.Tickets.SyncSource("ticket")}
}

// canSeeCodes reports whether the role may read booking and confirmation codes. Sync uses the same
// presenters, so an offline copy never carries a code a viewer may not see.
func canSeeCodes(role trip.Role) bool { return trip.Can(role, trip.ActionWriteContent) }

type FlightResponse struct {
	httpres.Meta
	Airline          string               `json:"airline,omitempty"`
	FlightNumber     string               `json:"flightNumber"`
	DepartureAirport string               `json:"departureAirport"`
	ArrivalAirport   string               `json:"arrivalAirport"`
	Departure        httpres.ZonedTimeDTO `json:"departure"`
	Arrival          httpres.ZonedTimeDTO `json:"arrival"`
	DurationMinutes  int                  `json:"durationMinutes"`
	Terminal         string               `json:"terminal,omitempty"`
	Gate             string               `json:"gate,omitempty"`
	Seat             string               `json:"seat,omitempty"`
	Baggage          string               `json:"baggage,omitempty"`
	BookingCode      string               `json:"bookingCode,omitempty"`
	Notes            string               `json:"notes,omitempty"`
}

func presentFlight(f booking.Flight, role trip.Role) FlightResponse {
	resp := FlightResponse{
		Meta: httpres.MetaOf(f.Base), Airline: f.Airline, FlightNumber: f.FlightNumber,
		DepartureAirport: f.DepartureAirport, ArrivalAirport: f.ArrivalAirport,
		Departure: *httpres.ZonedOf(&f.Departure), Arrival: *httpres.ZonedOf(&f.Arrival),
		DurationMinutes: int(f.Duration().Minutes()), Terminal: f.Terminal, Gate: f.Gate, Seat: f.Seat,
		Baggage: f.Baggage, Notes: f.Notes,
	}
	if canSeeCodes(role) {
		resp.BookingCode = f.BookingCode
	}
	return resp
}

type HotelResponse struct {
	httpres.Meta
	Name             string               `json:"name"`
	Location         *httpres.LocationDTO `json:"location,omitempty"`
	CheckIn          httpres.ZonedTimeDTO `json:"checkIn"`
	CheckOut         httpres.ZonedTimeDTO `json:"checkOut"`
	ConfirmationCode string               `json:"confirmationCode,omitempty"`
	ContactPhone     string               `json:"contactPhone,omitempty"`
	BookingURL       string               `json:"bookingUrl,omitempty"`
	Notes            string               `json:"notes,omitempty"`
}

func presentHotel(h booking.Hotel, role trip.Role) HotelResponse {
	resp := HotelResponse{
		Meta: httpres.MetaOf(h.Base), Name: h.Name, Location: httpres.LocationOf(h.Location),
		CheckIn: *httpres.ZonedOf(&h.CheckIn), CheckOut: *httpres.ZonedOf(&h.CheckOut),
		ContactPhone: h.ContactPhone, BookingURL: h.BookingURL, Notes: h.Notes,
	}
	if canSeeCodes(role) {
		resp.ConfirmationCode = h.ConfirmationCode
	}
	return resp
}

type TicketResponse struct {
	httpres.Meta
	Name             string                `json:"name"`
	Kind             string                `json:"kind"`
	Location         *httpres.LocationDTO  `json:"location,omitempty"`
	Start            *httpres.ZonedTimeDTO `json:"start,omitempty"`
	End              *httpres.ZonedTimeDTO `json:"end,omitempty"`
	Quantity         int                   `json:"quantity"`
	ConfirmationCode string                `json:"confirmationCode,omitempty"`
	Seat             string                `json:"seat,omitempty"`
	Cost             *httpres.MoneyDTO     `json:"cost,omitempty"`
	Status           string                `json:"status"`
	DocumentID       string                `json:"documentId,omitempty"`
	Notes            string                `json:"notes,omitempty"`
}

func presentTicket(t booking.Ticket, role trip.Role) TicketResponse {
	resp := TicketResponse{
		Meta: httpres.MetaOf(t.Base), Name: t.Name, Kind: string(t.Kind), Location: httpres.LocationOf(t.Location),
		Start: httpres.ZonedOf(t.Start), End: httpres.ZonedOf(t.End), Quantity: t.Quantity, Seat: t.Seat,
		Cost: httpres.MoneyOf(t.Cost), Status: string(t.Status), DocumentID: t.DocumentID, Notes: t.Notes,
	}
	if canSeeCodes(role) {
		resp.ConfirmationCode = t.ConfirmationCode
	}
	return resp
}
