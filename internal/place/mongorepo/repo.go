package mongorepo

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/place"
	"rinotravel-api/internal/resource/mongostore"
)

type placeDoc struct {
	Name            string                  `bson:"name"`
	Description     string                  `bson:"description,omitempty"`
	Category        string                  `bson:"category"`
	Priority        string                  `bson:"priority"`
	Notes           string                  `bson:"notes,omitempty"`
	ExternalID      string                  `bson:"externalId,omitempty"`
	Location        *mongostore.LocationDoc `bson:"location,omitempty"`
	DurationMinutes *int                    `bson:"durationMinutes,omitempty"`
	Cost            *mongostore.MoneyDoc    `bson:"cost,omitempty"`
}

var placeCodec = mongostore.Codec[place.Place]{
	Base: place.PlaceBase,
	Encode: func(p place.Place) bson.D {
		return mongostore.Marshal(placeDoc{
			Name: p.Name, Description: p.Description, Category: string(p.Category), Priority: string(p.Priority),
			Notes: p.Notes, ExternalID: p.ExternalID, Location: mongostore.LocationToDoc(p.Location),
			DurationMinutes: p.DurationMinutes, Cost: mongostore.MoneyToDoc(p.Cost),
		})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (place.Place, error) {
		var d placeDoc
		if err := bson.Unmarshal(raw, &d); err != nil {
			return place.Place{}, err
		}
		return place.Place{
			Base: base, Name: d.Name, Description: d.Description, Category: place.Category(d.Category),
			Priority: place.Priority(d.Priority), Notes: d.Notes, ExternalID: d.ExternalID,
			Location: d.Location.ToLocation(), DurationMinutes: d.DurationMinutes, Cost: d.Cost.ToMoney(),
		}, nil
	},
}

func NewPlaceStore(db *mongo.Database) *mongostore.Store[place.Place] {
	return mongostore.New(db, "places", placeCodec)
}

type restaurantDoc struct {
	Name            string                  `bson:"name"`
	Cuisine         string                  `bson:"cuisine,omitempty"`
	Notes           string                  `bson:"notes,omitempty"`
	Status          string                  `bson:"status"`
	ExternalID      string                  `bson:"externalId,omitempty"`
	ReservationCode string                  `bson:"reservationCode,omitempty"`
	DesiredDishes   []string                `bson:"desiredDishes,omitempty"`
	Location        *mongostore.LocationDoc `bson:"location,omitempty"`
	Cost            *mongostore.MoneyDoc    `bson:"cost,omitempty"`
	ReservationAt   *mongostore.ZonedDoc    `bson:"reservationAt,omitempty"`
}

var restaurantCodec = mongostore.Codec[place.Restaurant]{
	Base: place.RestaurantBase,
	Encode: func(r place.Restaurant) bson.D {
		return mongostore.Marshal(restaurantDoc{
			Name: r.Name, Cuisine: r.Cuisine, Notes: r.Notes, Status: string(r.Status), ExternalID: r.ExternalID,
			ReservationCode: r.ReservationCode, DesiredDishes: r.DesiredDishes,
			Location: mongostore.LocationToDoc(r.Location), Cost: mongostore.MoneyToDoc(r.Cost),
			ReservationAt: mongostore.ZonedToDoc(r.ReservationAt),
		})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (place.Restaurant, error) {
		var d restaurantDoc
		if err := bson.Unmarshal(raw, &d); err != nil {
			return place.Restaurant{}, err
		}
		return place.Restaurant{
			Base: base, Name: d.Name, Cuisine: d.Cuisine, Notes: d.Notes, Status: place.RestaurantStatus(d.Status),
			ExternalID: d.ExternalID, ReservationCode: d.ReservationCode, DesiredDishes: d.DesiredDishes,
			Location: d.Location.ToLocation(), Cost: d.Cost.ToMoney(), ReservationAt: d.ReservationAt.ToZoned(),
		}, nil
	},
}

func NewRestaurantStore(db *mongo.Database) *mongostore.Store[place.Restaurant] {
	return mongostore.New(db, "restaurants", restaurantCodec)
}
