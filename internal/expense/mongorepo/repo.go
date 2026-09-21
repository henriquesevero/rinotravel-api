package mongorepo

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/expense"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource/mongostore"
)

type expenseDoc struct {
	Name     string               `bson:"name"`
	Category string               `bson:"category"`
	Status   string               `bson:"status"`
	Estimate *mongostore.MoneyDoc `bson:"estimate,omitempty"`
	Actual   *mongostore.MoneyDoc `bson:"actual,omitempty"`
	Date     string               `bson:"date,omitempty"`
	LinkType string               `bson:"linkType,omitempty"`
	LinkID   string               `bson:"linkId,omitempty"`
	Notes    string               `bson:"notes,omitempty"`
}

var expenseCodec = mongostore.Codec[expense.Expense]{
	Base: expense.Base,
	Encode: func(e expense.Expense) bson.D {
		return mongostore.Marshal(expenseDoc{
			Name: e.Name, Category: string(e.Category), Status: string(e.Status),
			Estimate: mongostore.MoneyToDoc(e.Estimate), Actual: mongostore.MoneyToDoc(e.Actual),
			Date: string(e.Date), LinkType: e.LinkType, LinkID: e.LinkID, Notes: e.Notes,
		})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (expense.Expense, error) {
		var d expenseDoc
		if err := bson.Unmarshal(raw, &d); err != nil {
			return expense.Expense{}, err
		}
		return expense.Expense{
			Base: base, Name: d.Name, Category: expense.Category(d.Category), Status: expense.Status(d.Status),
			Estimate: d.Estimate.ToMoney(), Actual: d.Actual.ToMoney(), Date: kernel.Date(d.Date),
			LinkType: d.LinkType, LinkID: d.LinkID, Notes: d.Notes,
		}, nil
	},
}

func NewExpenseStore(db *mongo.Database) *mongostore.Store[expense.Expense] {
	return mongostore.New(db, "expenses", expenseCodec)
}

type limitDoc struct {
	Category string              `bson:"category"`
	Amount   mongostore.MoneyDoc `bson:"amount"`
}

var limitCodec = mongostore.Codec[expense.Limit]{
	Base: expense.LimitBase,
	Encode: func(l expense.Limit) bson.D {
		return mongostore.Marshal(limitDoc{Category: l.Category, Amount: *mongostore.MoneyToDoc(&l.Amount)})
	},
	Decode: func(raw bson.Raw, base kernel.Base) (expense.Limit, error) {
		var d limitDoc
		if err := bson.Unmarshal(raw, &d); err != nil {
			return expense.Limit{}, err
		}
		return expense.Limit{Base: base, Category: d.Category, Amount: *d.Amount.ToMoney()}, nil
	},
}

func NewLimitStore(db *mongo.Database) *mongostore.Store[expense.Limit] {
	return mongostore.New(db, "budget_limits", limitCodec)
}

// LimitIndexes keeps one budget per category in a trip.
func LimitIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{mongostore.UniqueAmongLive(bson.E{Key: "category", Value: 1})}
}
