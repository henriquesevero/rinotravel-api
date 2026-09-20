package mongostore

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rinotravel-api/internal/kernel"
)

// Marshal turns a document struct into the bson.D a Codec.Encode returns.
func Marshal(doc any) bson.D {
	raw, err := bson.Marshal(doc)
	if err != nil {
		panic("mongostore: cannot marshal document: " + err.Error())
	}
	var out bson.D
	if err := bson.Unmarshal(raw, &out); err != nil {
		panic("mongostore: cannot re-read document: " + err.Error())
	}
	return out
}

// ZonedDoc stores an event moment as its UTC instant plus the IANA zone of the place.
type ZonedDoc struct {
	At   time.Time `bson:"at"`
	Zone string    `bson:"zone"`
}

func ZonedToDoc(z *kernel.ZonedTime) *ZonedDoc {
	if z == nil {
		return nil
	}
	return &ZonedDoc{At: z.Instant, Zone: string(z.Zone)}
}

func (d *ZonedDoc) ToZoned() *kernel.ZonedTime {
	if d == nil {
		return nil
	}
	return &kernel.ZonedTime{Instant: d.At.UTC(), Zone: kernel.Timezone(d.Zone)}
}

type LocationDoc struct {
	Name    string   `bson:"name,omitempty"`
	Address string   `bson:"address,omitempty"`
	Lat     *float64 `bson:"lat,omitempty"`
	Lng     *float64 `bson:"lng,omitempty"`
}

func LocationToDoc(l kernel.Location) *LocationDoc {
	if l.IsZero() {
		return nil
	}
	doc := &LocationDoc{Name: l.Name, Address: l.Address}
	if l.Coordinates != nil {
		lat, lng := l.Coordinates.Lat, l.Coordinates.Lng
		doc.Lat, doc.Lng = &lat, &lng
	}
	return doc
}

func (d *LocationDoc) ToLocation() kernel.Location {
	if d == nil {
		return kernel.Location{}
	}
	loc := kernel.Location{Name: d.Name, Address: d.Address}
	if d.Lat != nil && d.Lng != nil {
		loc.Coordinates = &kernel.Coordinates{Lat: *d.Lat, Lng: *d.Lng}
	}
	return loc
}

type MoneyDoc struct {
	Amount   int64  `bson:"amount"`
	Currency string `bson:"currency"`
}

func MoneyToDoc(m *kernel.Money) *MoneyDoc {
	if m == nil {
		return nil
	}
	return &MoneyDoc{Amount: m.Amount, Currency: string(m.Currency)}
}

func (d *MoneyDoc) ToMoney() *kernel.Money {
	if d == nil {
		return nil
	}
	return &kernel.Money{Amount: d.Amount, Currency: kernel.Currency(d.Currency)}
}
