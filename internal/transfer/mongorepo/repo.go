package mongorepo

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource/mongostore"
	"rinotravel-api/internal/transfer"
)

type legDoc struct {
	Mode            string                  `bson:"mode"`
	Origin          *mongostore.LocationDoc `bson:"origin,omitempty"`
	Destination     *mongostore.LocationDoc `bson:"destination,omitempty"`
	Departure       *mongostore.ZonedDoc    `bson:"departure,omitempty"`
	Arrival         *mongostore.ZonedDoc    `bson:"arrival,omitempty"`
	DurationMinutes *int                    `bson:"durationMinutes,omitempty"`
	Line            string                  `bson:"line,omitempty"`
	Direction       string                  `bson:"direction,omitempty"`
	Stops           *int                    `bson:"stops,omitempty"`
	Instructions    string                  `bson:"instructions,omitempty"`
	Cost            *mongostore.MoneyDoc    `bson:"cost,omitempty"`
}

type transferDoc struct {
	Origin          *mongostore.LocationDoc `bson:"origin,omitempty"`
	Destination     *mongostore.LocationDoc `bson:"destination,omitempty"`
	Status          string                  `bson:"status"`
	RouteProvider   string                  `bson:"routeProvider,omitempty"`
	ExternalRouteID string                  `bson:"externalRouteId,omitempty"`
	Notes           string                  `bson:"notes,omitempty"`
	Legs            []legDoc                `bson:"legs"`
}

var codec = mongostore.Codec[transfer.Transfer]{
	Base: transfer.Base,
	Encode: func(t transfer.Transfer) bson.D {
		legs := make([]legDoc, 0, len(t.Legs))
		for _, l := range t.Legs {
			legs = append(legs, legDoc{
				Mode: string(l.Mode), Origin: mongostore.LocationToDoc(l.Origin), Destination: mongostore.LocationToDoc(l.Destination),
				Departure: mongostore.ZonedToDoc(l.Departure), Arrival: mongostore.ZonedToDoc(l.Arrival),
				DurationMinutes: l.DurationMinutes, Line: l.Line, Direction: l.Direction, Stops: l.Stops,
				Instructions: l.Instructions, Cost: mongostore.MoneyToDoc(l.Cost),
			})
		}
		return mongostore.Marshal(transferDoc{
			Origin: mongostore.LocationToDoc(t.Origin), Destination: mongostore.LocationToDoc(t.Destination),
			Status: string(t.Status), RouteProvider: t.RouteProvider, ExternalRouteID: t.ExternalRouteID,
			Notes: t.Notes, Legs: legs,
		})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (transfer.Transfer, error) {
		var d transferDoc
		if err := bson.Unmarshal(raw, &d); err != nil {
			return transfer.Transfer{}, err
		}
		legs := make([]transfer.Leg, 0, len(d.Legs))
		for _, l := range d.Legs {
			legs = append(legs, transfer.Leg{
				Mode: transfer.Mode(l.Mode), Origin: l.Origin.ToLocation(), Destination: l.Destination.ToLocation(),
				Departure: l.Departure.ToZoned(), Arrival: l.Arrival.ToZoned(), DurationMinutes: l.DurationMinutes,
				Line: l.Line, Direction: l.Direction, Stops: l.Stops, Instructions: l.Instructions, Cost: l.Cost.ToMoney(),
			})
		}
		return transfer.Transfer{
			Base: base, Origin: d.Origin.ToLocation(), Destination: d.Destination.ToLocation(),
			Status: kernel.PlanStatus(d.Status), RouteProvider: d.RouteProvider, ExternalRouteID: d.ExternalRouteID,
			Notes: d.Notes, Legs: legs,
		}, nil
	},
}

func NewStore(db *mongo.Database) *mongostore.Store[transfer.Transfer] {
	return mongostore.New(db, "transfers", codec)
}
