package mongorepo

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/booking"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource/mongostore"
)

type flightDoc struct {
	Airline          string              `bson:"airline,omitempty"`
	FlightNumber     string              `bson:"flightNumber"`
	DepartureAirport string              `bson:"departureAirport"`
	ArrivalAirport   string              `bson:"arrivalAirport"`
	Departure        mongostore.ZonedDoc `bson:"departure"`
	Arrival          mongostore.ZonedDoc `bson:"arrival"`
	Terminal         string              `bson:"terminal,omitempty"`
	Gate             string              `bson:"gate,omitempty"`
	Seat             string              `bson:"seat,omitempty"`
	Baggage          string              `bson:"baggage,omitempty"`
	BookingCode      string              `bson:"bookingCode,omitempty"`
	Notes            string              `bson:"notes,omitempty"`
}

var flightCodec = mongostore.Codec[booking.Flight]{
	Base: booking.FlightBase,
	Encode: func(f booking.Flight) bson.D {
		return mongostore.Marshal(flightDoc{
			Airline: f.Airline, FlightNumber: f.FlightNumber, DepartureAirport: f.DepartureAirport,
			ArrivalAirport: f.ArrivalAirport, Departure: *mongostore.ZonedToDoc(&f.Departure),
			Arrival: *mongostore.ZonedToDoc(&f.Arrival), Terminal: f.Terminal, Gate: f.Gate, Seat: f.Seat,
			Baggage: f.Baggage, BookingCode: f.BookingCode, Notes: f.Notes,
		})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (booking.Flight, error) {
		var d flightDoc
		if err := bson.Unmarshal(raw, &d); err != nil {
			return booking.Flight{}, err
		}
		return booking.Flight{
			Base: base, Airline: d.Airline, FlightNumber: d.FlightNumber, DepartureAirport: d.DepartureAirport,
			ArrivalAirport: d.ArrivalAirport, Departure: *d.Departure.ToZoned(), Arrival: *d.Arrival.ToZoned(),
			Terminal: d.Terminal, Gate: d.Gate, Seat: d.Seat, Baggage: d.Baggage, BookingCode: d.BookingCode, Notes: d.Notes,
		}, nil
	},
}

func NewFlightStore(db *mongo.Database) *mongostore.Store[booking.Flight] {
	return mongostore.New(db, "flights", flightCodec)
}

type hotelDoc struct {
	Name             string                  `bson:"name"`
	Location         *mongostore.LocationDoc `bson:"location,omitempty"`
	CheckIn          mongostore.ZonedDoc     `bson:"checkIn"`
	CheckOut         mongostore.ZonedDoc     `bson:"checkOut"`
	ConfirmationCode string                  `bson:"confirmationCode,omitempty"`
	ContactPhone     string                  `bson:"contactPhone,omitempty"`
	BookingURL       string                  `bson:"bookingUrl,omitempty"`
	Notes            string                  `bson:"notes,omitempty"`
}

var hotelCodec = mongostore.Codec[booking.Hotel]{
	Base: booking.HotelBase,
	Encode: func(h booking.Hotel) bson.D {
		return mongostore.Marshal(hotelDoc{
			Name: h.Name, Location: mongostore.LocationToDoc(h.Location),
			CheckIn: *mongostore.ZonedToDoc(&h.CheckIn), CheckOut: *mongostore.ZonedToDoc(&h.CheckOut),
			ConfirmationCode: h.ConfirmationCode, ContactPhone: h.ContactPhone, BookingURL: h.BookingURL, Notes: h.Notes,
		})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (booking.Hotel, error) {
		var d hotelDoc
		if err := bson.Unmarshal(raw, &d); err != nil {
			return booking.Hotel{}, err
		}
		return booking.Hotel{
			Base: base, Name: d.Name, Location: d.Location.ToLocation(), CheckIn: *d.CheckIn.ToZoned(),
			CheckOut: *d.CheckOut.ToZoned(), ConfirmationCode: d.ConfirmationCode, ContactPhone: d.ContactPhone,
			BookingURL: d.BookingURL, Notes: d.Notes,
		}, nil
	},
}

func NewHotelStore(db *mongo.Database) *mongostore.Store[booking.Hotel] {
	return mongostore.New(db, "hotels", hotelCodec)
}

type ticketDoc struct {
	Name             string                  `bson:"name"`
	Kind             string                  `bson:"kind"`
	Location         *mongostore.LocationDoc `bson:"location,omitempty"`
	Start            *mongostore.ZonedDoc    `bson:"start,omitempty"`
	End              *mongostore.ZonedDoc    `bson:"end,omitempty"`
	Quantity         int                     `bson:"quantity"`
	ConfirmationCode string                  `bson:"confirmationCode,omitempty"`
	Seat             string                  `bson:"seat,omitempty"`
	Cost             *mongostore.MoneyDoc    `bson:"cost,omitempty"`
	Status           string                  `bson:"status"`
	DocumentID       string                  `bson:"documentId,omitempty"`
	Notes            string                  `bson:"notes,omitempty"`
}

var ticketCodec = mongostore.Codec[booking.Ticket]{
	Base: booking.TicketBase,
	Encode: func(t booking.Ticket) bson.D {
		return mongostore.Marshal(ticketDoc{
			Name: t.Name, Kind: string(t.Kind), Location: mongostore.LocationToDoc(t.Location),
			Start: mongostore.ZonedToDoc(t.Start), End: mongostore.ZonedToDoc(t.End), Quantity: t.Quantity,
			ConfirmationCode: t.ConfirmationCode, Seat: t.Seat, Cost: mongostore.MoneyToDoc(t.Cost),
			Status: string(t.Status), DocumentID: t.DocumentID, Notes: t.Notes,
		})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (booking.Ticket, error) {
		var d ticketDoc
		if err := bson.Unmarshal(raw, &d); err != nil {
			return booking.Ticket{}, err
		}
		return booking.Ticket{
			Base: base, Name: d.Name, Kind: booking.TicketKind(d.Kind), Location: d.Location.ToLocation(),
			Start: d.Start.ToZoned(), End: d.End.ToZoned(), Quantity: d.Quantity,
			ConfirmationCode: d.ConfirmationCode, Seat: d.Seat, Cost: d.Cost.ToMoney(),
			Status: kernel.PlanStatus(d.Status), DocumentID: d.DocumentID, Notes: d.Notes,
		}, nil
	},
}

func NewTicketStore(db *mongo.Database) *mongostore.Store[booking.Ticket] {
	return mongostore.New(db, "tickets", ticketCodec)
}
