package mongorepo

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/checklist"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource/mongostore"
)

type itemDoc struct {
	Title    string `bson:"title"`
	Category string `bson:"category"`
	Checked  bool   `bson:"checked"`
	Quantity int    `bson:"quantity"`
	Notes    string `bson:"notes,omitempty"`
}

var itemCodec = mongostore.Codec[checklist.Item]{
	Base: checklist.ItemBase,
	Encode: func(i checklist.Item) bson.D {
		return mongostore.Marshal(itemDoc{
			Title: i.Title, Category: string(i.Category), Checked: i.Checked,
			Quantity: i.Quantity, Notes: i.Notes,
		})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (checklist.Item, error) {
		var doc itemDoc
		if err := bson.Unmarshal(raw, &doc); err != nil {
			return checklist.Item{}, err
		}
		return checklist.Item{
			Base: base, Title: doc.Title, Category: checklist.Category(doc.Category),
			Checked: doc.Checked, Quantity: doc.Quantity, Notes: doc.Notes,
		}, nil
	},
}

func NewItemStore(db *mongo.Database) *mongostore.Store[checklist.Item] {
	return mongostore.New(db, "checklist_items", itemCodec)
}
