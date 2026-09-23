// Package checklist is the trip's packing list: plain, manually kept items to bring or do before the
// trip, grouped by category and ticked off as they are packed. Nothing here is suggested
// automatically; every item is typed in by a person.
package checklist

import (
	"context"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type Category string

const (
	CategoryDocuments   Category = "DOCUMENTS"
	CategoryClothes     Category = "CLOTHES"
	CategoryElectronics Category = "ELECTRONICS"
	CategoryToiletries  Category = "TOILETRIES"
	CategoryOther       Category = "OTHER"
)

var validCategories = map[Category]bool{
	CategoryDocuments: true, CategoryClothes: true, CategoryElectronics: true,
	CategoryToiletries: true, CategoryOther: true,
}

const defaultQuantity = 1

// Item is one thing to pack or do before the trip. It carries no schedule and no place: it is
// checked off, not timed.
type Item struct {
	kernel.Base
	Title    string
	Category Category
	Checked  bool
	Quantity int
	Notes    string
}

func ItemBase(i *Item) *kernel.Base { return &i.Base }

type ItemCreate struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Quantity int    `json:"quantity"`
	Notes    string `json:"notes"`
}

type ItemPatch struct {
	kernel.Versioned
	Title    *string `json:"title"`
	Category *string `json:"category"`
	Checked  *bool   `json:"checked"`
	Quantity *int    `json:"quantity"`
	Notes    *string `json:"notes"`
}

type Items struct {
	res *resource.Service[Item]
}

func NewItems(repo resource.Repo[Item], authz resource.Authorizer) *Items {
	return &Items{
		res: resource.NewService[Item](repo, authz, resource.Config[Item]{Name: "checklist_item", Base: ItemBase}),
	}
}

func (s *Items) Resource() *resource.Service[Item] { return s.res }

func (s *Items) Create(ctx context.Context, actor user.ID, tripID trip.ID, in ItemCreate) (resource.Result[Item], error) {
	return s.res.Create(ctx, actor, tripID, in.ID, func(_ trip.Access) (Item, error) {
		var v kernel.Validator
		title := v.Text("title", in.Title, true, 200)
		category := parseCategory(&v, in.Category, CategoryOther)
		quantity := quantityOf(&v, in.Quantity)
		item := Item{
			Title: title, Category: category, Quantity: quantity,
			Notes: v.Text("notes", in.Notes, false, 500),
		}
		return item, v.Err()
	})
}

func (s *Items) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p ItemPatch) (resource.Result[Item], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Item]{}, err
	}
	return s.res.Update(ctx, actor, tripID, id, version, func(cur Item, _ trip.Access) (Item, error) {
		var v kernel.Validator
		if p.Title != nil {
			cur.Title = v.Text("title", *p.Title, true, 200)
		}
		if p.Category != nil {
			cur.Category = parseCategory(&v, *p.Category, cur.Category)
		}
		if p.Checked != nil {
			cur.Checked = *p.Checked
		}
		if p.Quantity != nil {
			cur.Quantity = quantityOf(&v, *p.Quantity)
		}
		if p.Notes != nil {
			cur.Notes = v.Text("notes", *p.Notes, false, 500)
		}
		return cur, v.Err()
	})
}

func parseCategory(v *kernel.Validator, in string, fallback Category) Category {
	if in == "" {
		return fallback
	}
	category := Category(in)
	if !validCategories[category] {
		v.Add("category", "must be one of DOCUMENTS, CLOTHES, ELECTRONICS, TOILETRIES, OTHER")
		return fallback
	}
	return category
}

// quantityOf shares the validator's error-collecting style: 0 means "not sent", the default;
// anything else must be a positive count.
func quantityOf(v *kernel.Validator, in int) int {
	if in == 0 {
		return defaultQuantity
	}
	v.Check(in > 0, "quantity", "must be at least 1")
	return in
}
