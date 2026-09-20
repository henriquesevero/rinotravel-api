package mongorepo

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/itinerary"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource/mongostore"
)

type dayDoc struct {
	Date  string `bson:"date"`
	Title string `bson:"title,omitempty"`
	Notes string `bson:"notes,omitempty"`
}

var dayCodec = mongostore.Codec[itinerary.Day]{
	Base: itinerary.DayBase,
	Encode: func(d itinerary.Day) bson.D {
		return mongostore.Marshal(dayDoc{Date: string(d.Date), Title: d.Title, Notes: d.Notes})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (itinerary.Day, error) {
		var doc dayDoc
		err := bson.Unmarshal(raw, &doc)
		return itinerary.Day{Base: base, Date: kernel.Date(doc.Date), Title: doc.Title, Notes: doc.Notes}, err
	},
}

// NewDayStore keeps one live day per date and trip.
func NewDayStore(db *mongo.Database) *mongostore.Store[itinerary.Day] {
	return mongostore.New(db, "itinerary_days", dayCodec)
}

func DayIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{mongostore.UniqueAmongLive(bson.E{Key: "date", Value: 1})}
}

type itemDoc struct {
	DayID           string                  `bson:"dayId"`
	Title           string                  `bson:"title"`
	Description     string                  `bson:"description,omitempty"`
	Category        string                  `bson:"category"`
	Status          string                  `bson:"status"`
	Notes           string                  `bson:"notes,omitempty"`
	PlaceID         string                  `bson:"placeId,omitempty"`
	Position        int                     `bson:"position"`
	Start           *mongostore.ZonedDoc    `bson:"start,omitempty"`
	End             *mongostore.ZonedDoc    `bson:"end,omitempty"`
	Location        *mongostore.LocationDoc `bson:"location,omitempty"`
	DurationMinutes *int                    `bson:"durationMinutes,omitempty"`
	Cost            *mongostore.MoneyDoc    `bson:"cost,omitempty"`
}

var itemCodec = mongostore.Codec[itinerary.Item]{
	Base: itinerary.ItemBase,
	Encode: func(i itinerary.Item) bson.D {
		return mongostore.Marshal(itemDoc{
			DayID: i.DayID, Title: i.Title, Description: i.Description, Category: string(i.Category),
			Status: string(i.Status), Notes: i.Notes, PlaceID: i.PlaceID, Position: i.Position,
			Start: mongostore.ZonedToDoc(i.Start), End: mongostore.ZonedToDoc(i.End),
			Location: mongostore.LocationToDoc(i.Location), DurationMinutes: i.DurationMinutes,
			Cost: mongostore.MoneyToDoc(i.Cost),
		})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (itinerary.Item, error) {
		var doc itemDoc
		if err := bson.Unmarshal(raw, &doc); err != nil {
			return itinerary.Item{}, err
		}
		return itinerary.Item{
			Base: base, DayID: doc.DayID, Title: doc.Title, Description: doc.Description,
			Category: itinerary.Category(doc.Category), Status: kernel.PlanStatus(doc.Status),
			Notes: doc.Notes, PlaceID: doc.PlaceID, Position: doc.Position,
			Start: doc.Start.ToZoned(), End: doc.End.ToZoned(), Location: doc.Location.ToLocation(),
			DurationMinutes: doc.DurationMinutes, Cost: doc.Cost.ToMoney(),
		}, nil
	},
}

func NewItemStore(db *mongo.Database) *mongostore.Store[itinerary.Item] {
	return mongostore.New(db, "itinerary_items", itemCodec)
}
