package itinerary

import (
	"context"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

type Day struct {
	kernel.Base
	Date  kernel.Date
	Title string
	Notes string
}

func DayBase(d *Day) *kernel.Base { return &d.Base }

type DayCreate struct {
	ID    string `json:"id"`
	Date  string `json:"date"`
	Title string `json:"title"`
	Notes string `json:"notes"`
}

type DayPatch struct {
	kernel.Versioned
	Title *string `json:"title"`
	Notes *string `json:"notes"`
}

type Days struct {
	res   *resource.Service[Day]
	items resource.Repo[Item]
	authz resource.Authorizer
}

func NewDays(repo resource.Repo[Day], items resource.Repo[Item], authz resource.Authorizer) *Days {
	return &Days{
		res:   resource.NewService[Day](repo, authz, resource.Config[Day]{Name: "itinerary_day", Base: DayBase}),
		items: items,
		authz: authz,
	}
}

func (d *Days) Resource() *resource.Service[Day] { return d.res }

func (d *Days) Create(ctx context.Context, actor user.ID, tripID trip.ID, in DayCreate) (resource.Result[Day], error) {
	return d.res.Create(ctx, actor, tripID, in.ID, func(access trip.Access) (Day, error) {
		var v kernel.Validator
		date, err := kernel.ParseDate(in.Date)
		if err != nil {
			v.Add("date", err.Error())
		} else {
			v.Check(date >= access.Trip.StartDate && date <= access.Trip.EndDate, "date", "must fall within the trip's dates")
		}
		day := Day{
			Date:  date,
			Title: v.Text("title", in.Title, false, 100),
			Notes: v.Text("notes", in.Notes, false, 2000),
		}
		if err := v.Err(); err != nil {
			return Day{}, err
		}

		existing, err := d.res.Repo().List(ctx, string(tripID))
		if err != nil {
			return Day{}, err
		}
		for _, other := range existing {
			if other.Date == date {
				return Day{}, apperror.Conflict("day_exists", "This trip already has a day for that date.")
			}
		}
		return day, nil
	})
}

func (d *Days) Update(ctx context.Context, actor user.ID, tripID trip.ID, id string, p DayPatch) (resource.Result[Day], error) {
	version, err := p.Require()
	if err != nil {
		return resource.Result[Day]{}, err
	}
	return d.res.Update(ctx, actor, tripID, id, version, func(cur Day, _ trip.Access) (Day, error) {
		var v kernel.Validator
		if p.Title != nil {
			cur.Title = v.Text("title", *p.Title, false, 100)
		}
		if p.Notes != nil {
			cur.Notes = v.Text("notes", *p.Notes, false, 2000)
		}
		return cur, v.Err()
	})
}

// Delete refuses to drop a day that still has items, so nothing is orphaned silently.
func (d *Days) Delete(ctx context.Context, actor user.ID, tripID trip.ID, id string, baseVersion *int64) error {
	if _, err := d.authz.Authorize(ctx, tripID, actor, trip.ActionWriteContent); err != nil {
		return err
	}
	items, err := d.items.List(ctx, string(tripID))
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.DayID == id {
			return apperror.Conflict("day_not_empty", "Remove or move the day's items before deleting it.")
		}
	}
	return d.res.Delete(ctx, actor, tripID, id, baseVersion)
}
