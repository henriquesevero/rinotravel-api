package itinerary_test

import (
	"context"
	"errors"
	"testing"

	"rinotravel-api/internal/apperror"
	"rinotravel-api/internal/itinerary"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/resource/resourcetest"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/user"
)

const tripID = trip.ID("trip-1")

type env struct {
	days     *itinerary.Days
	items    *itinerary.Items
	timeline *itinerary.Timeline
}

func newEnv(sources ...itinerary.EntrySource) env {
	authz := resourcetest.NewAuthz(map[user.ID]trip.Role{"owner": trip.RoleOwner, "member": trip.RoleMember, "viewer": trip.RoleViewer})
	dayRepo := resourcetest.New(itinerary.DayBase).WithUnique(func(a, b itinerary.Day) bool { return a.Date == b.Date })
	itemRepo := resourcetest.New(itinerary.ItemBase)
	return env{
		days:     itinerary.NewDays(dayRepo, itemRepo, authz),
		items:    itinerary.NewItems(itemRepo, dayRepo, authz),
		timeline: itinerary.NewTimeline(dayRepo, itemRepo, authz, sources...),
	}
}

func str(s string) *string { return &s }
func num(n int) *int       { return &n }

func requireApp(t *testing.T, err error, kind apperror.Kind, code string) *apperror.Error {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("error = %v, want kind %d code %s", err, kind, code)
	}
	return appErr
}

func invalidFields(t *testing.T, err error) map[string]bool {
	t.Helper()
	appErr := requireApp(t, err, apperror.KindValidation, "validation_failed")
	out := map[string]bool{}
	for _, f := range appErr.Fields {
		out[f.Field] = true
	}
	return out
}

func (e env) day(t *testing.T, date string) itinerary.Day {
	t.Helper()
	res, err := e.days.Create(context.Background(), "member", tripID, itinerary.DayCreate{Date: date})
	if err != nil {
		t.Fatalf("create day %s: %v", date, err)
	}
	return res.Entity
}

func zt(dateTime string) kernel.Optional[kernel.ZonedTimeInput] {
	return kernel.Optional[kernel.ZonedTimeInput]{Set: true, Value: kernel.ZonedTimeInput{DateTime: dateTime}}
}

func clear[T any]() kernel.Optional[T] { return kernel.Optional[T]{Set: true, Clear: true} }

func (e env) item(t *testing.T, dayID, title string, mutate ...func(*itinerary.ItemCreate)) itinerary.Item {
	t.Helper()
	in := itinerary.ItemCreate{ItemFields: itinerary.ItemFields{DayID: &dayID, Title: &title, Category: str("ATTRACTION")}}
	for _, m := range mutate {
		m(&in)
	}
	res, err := e.items.Create(context.Background(), "member", tripID, in)
	if err != nil {
		t.Fatalf("create item %s: %v", title, err)
	}
	return res.Entity
}

func TestCreateDay(t *testing.T) {
	ctx := context.Background()

	t.Run("creates a day inside the trip's dates", func(t *testing.T) {
		e := newEnv()

		res, err := e.days.Create(ctx, "member", tripID, itinerary.DayCreate{Date: "2027-04-03", Title: " Asakusa ", Notes: "temple day"})

		if err != nil || res.Entity.Date != "2027-04-03" || res.Entity.Title != "Asakusa" || res.Entity.Version != 1 || res.Entity.TripID != "trip-1" {
			t.Errorf("Create() = %+v, %v", res.Entity, err)
		}
	})

	t.Run("rejects dates outside the trip and malformed dates", func(t *testing.T) {
		e := newEnv()
		for _, date := range []string{"2027-03-31", "2027-04-16", "2027-02-30", "", "01/04/2027"} {
			_, err := e.days.Create(ctx, "member", tripID, itinerary.DayCreate{Date: date})
			if !invalidFields(t, err)["date"] {
				t.Errorf("date %q was accepted", date)
			}
		}
	})

	t.Run("one day per date", func(t *testing.T) {
		e := newEnv()
		e.day(t, "2027-04-03")

		_, err := e.days.Create(ctx, "owner", tripID, itinerary.DayCreate{Date: "2027-04-03"})

		requireApp(t, err, apperror.KindConflict, "day_exists")
	})

	t.Run("permissions", func(t *testing.T) {
		e := newEnv()
		_, err := e.days.Create(ctx, "viewer", tripID, itinerary.DayCreate{Date: "2027-04-03"})
		requireApp(t, err, apperror.KindForbidden, "forbidden")
		_, err = e.days.Create(ctx, "stranger", tripID, itinerary.DayCreate{Date: "2027-04-03"})
		requireApp(t, err, apperror.KindNotFound, "trip_not_found")
	})
}

func TestUpdateAndDeleteDay(t *testing.T) {
	ctx := context.Background()

	t.Run("updates title and notes, guarded by the version", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")
		p := itinerary.DayPatch{Title: str("Renamed")}
		p.SetVersion(1)

		res, err := e.days.Update(ctx, "member", tripID, d.ID, p)
		if err != nil || res.Entity.Title != "Renamed" || res.Entity.Version != 2 {
			t.Fatalf("Update() = %+v, %v", res.Entity, err)
		}
		_, err = e.days.Update(ctx, "member", tripID, d.ID, p)
		requireApp(t, err, apperror.KindConflict, "version_conflict")

		_, err = e.days.Update(ctx, "member", tripID, d.ID, itinerary.DayPatch{Title: str("x")})
		if !invalidFields(t, err)["baseVersion"] {
			t.Error("a missing baseVersion must be reported")
		}
	})

	t.Run("a day with items cannot be deleted until it is emptied", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")
		item := e.item(t, d.ID, "Senso-ji")

		requireApp(t, e.days.Delete(ctx, "member", tripID, d.ID, nil), apperror.KindConflict, "day_not_empty")

		if err := e.items.Resource().Delete(ctx, "member", tripID, item.ID, nil); err != nil {
			t.Fatal(err)
		}
		if err := e.days.Delete(ctx, "member", tripID, d.ID, nil); err != nil {
			t.Errorf("deleting an empty day: %v", err)
		}
		e.day(t, "2027-04-03")
	})

	t.Run("deleting checks permissions before revealing anything", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")

		requireApp(t, e.days.Delete(ctx, "viewer", tripID, d.ID, nil), apperror.KindForbidden, "forbidden")
		requireApp(t, e.days.Delete(ctx, "stranger", tripID, d.ID, nil), apperror.KindNotFound, "trip_not_found")
	})
}

func TestCreateItem(t *testing.T) {
	ctx := context.Background()

	t.Run("minimal item gets defaults and successive positions", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")

		first := e.item(t, d.ID, "One")
		second := e.item(t, d.ID, "Two")

		if first.Status != kernel.StatusPlanned || first.Position != 0 || second.Position != 1 || first.Start != nil {
			t.Errorf("unexpected defaults: %+v / %+v", first, second)
		}
	})

	t.Run("times default to the trip's zone and must fall on the day", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")

		item := e.item(t, d.ID, "Temple", func(in *itinerary.ItemCreate) {
			in.Start = zt("2027-04-03T09:30")
			in.End = zt("2027-04-03T11:00")
		})

		if item.Start.Zone != "Asia/Tokyo" || item.Start.LocalString() != "2027-04-03T09:30" {
			t.Errorf("start = %+v", item.Start)
		}
		if d, ok := item.EffectiveDuration(); !ok || d.Minutes() != 90 {
			t.Errorf("EffectiveDuration() = %v, %v; want 90 minutes derived from the times", d, ok)
		}

		wrongDay := "2027-04-05T09:30"
		_, err := e.items.Create(ctx, "member", tripID, itinerary.ItemCreate{ItemFields: itinerary.ItemFields{
			DayID: &d.ID, Title: str("x"), Category: str("OTHER"), Start: zt(wrongDay),
		}})
		if !invalidFields(t, err)["start.dateTime"] {
			t.Error("a start on another date must be rejected")
		}
	})

	t.Run("the local day decides, not UTC", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")

		// 00:30 in Tokyo is still April 3 locally although it is April 2 in UTC.
		e.item(t, d.ID, "Early", func(in *itinerary.ItemCreate) { in.Start = zt("2027-04-03T00:30") })
	})

	t.Run("an item may cross midnight but not end before it starts", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")

		e.item(t, d.ID, "Night out", func(in *itinerary.ItemCreate) {
			in.Start = zt("2027-04-03T22:00")
			in.End = zt("2027-04-04T02:00")
		})
		_, err := e.items.Create(ctx, "member", tripID, itinerary.ItemCreate{ItemFields: itinerary.ItemFields{
			DayID: &d.ID, Title: str("Backwards"), Category: str("OTHER"), Start: zt("2027-04-03T12:00"), End: zt("2027-04-03T11:00"),
		}})
		if !invalidFields(t, err)["end.dateTime"] {
			t.Error("an end before the start must be rejected")
		}
	})

	t.Run("cross-field rules", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")
		base := func() itinerary.ItemFields {
			return itinerary.ItemFields{DayID: &d.ID, Title: str("x"), Category: str("OTHER")}
		}

		endOnly := base()
		endOnly.End = zt("2027-04-03T11:00")
		_, err := e.items.Create(ctx, "member", tripID, itinerary.ItemCreate{ItemFields: endOnly})
		if !invalidFields(t, err)["end"] {
			t.Error("an end without a start must be rejected")
		}

		both := base()
		both.Start, both.End, both.DurationMinutes = zt("2027-04-03T10:00"), zt("2027-04-03T11:00"), kernel.Optional[int]{Set: true, Value: 30}
		_, err = e.items.Create(ctx, "member", tripID, itinerary.ItemCreate{ItemFields: both})
		if !invalidFields(t, err)["estimatedDurationMinutes"] {
			t.Error("an estimate next to an end time duplicates the source of truth")
		}

		estimate := base()
		estimate.Start, estimate.DurationMinutes = zt("2027-04-03T10:00"), kernel.Optional[int]{Set: true, Value: 45}
		item, err := e.items.Create(ctx, "member", tripID, itinerary.ItemCreate{ItemFields: estimate})
		if err != nil {
			t.Fatal(err)
		}
		if d, ok := item.Entity.EffectiveDuration(); !ok || d.Minutes() != 45 {
			t.Errorf("EffectiveDuration() = %v, %v; want the estimate", d, ok)
		}
	})

	t.Run("field validation reports everything at once", func(t *testing.T) {
		e := newEnv()

		_, err := e.items.Create(ctx, "member", tripID, itinerary.ItemCreate{})

		fields := invalidFields(t, err)
		for _, want := range []string{"dayId", "title", "category"} {
			if !fields[want] {
				t.Errorf("missing error for %s in %v", want, fields)
			}
		}

		d := e.day(t, "2027-04-03")
		lat, lng := 95.0, 10.0
		_, err = e.items.Create(ctx, "member", tripID, itinerary.ItemCreate{ItemFields: itinerary.ItemFields{
			DayID: &d.ID, Title: str("x"), Category: str("FLIGHT"), Status: str("DONE"),
			Location: kernel.Optional[kernel.LocationInput]{Set: true, Value: kernel.LocationInput{Latitude: &lat, Longitude: &lng}},
			Cost:     kernel.Optional[kernel.MoneyInput]{Set: true, Value: kernel.MoneyInput{Amount: -5, Currency: "ZZZ"}},
		}})
		fields = invalidFields(t, err)
		for _, want := range []string{"category", "status", "location.latitude", "estimatedCost.amount", "estimatedCost.currency"} {
			if !fields[want] {
				t.Errorf("missing error for %s in %v", want, fields)
			}
		}
	})

	t.Run("a day from another trip cannot be used", func(t *testing.T) {
		e := newEnv()
		other, err := e.days.Create(ctx, "member", "trip-2", itinerary.DayCreate{Date: "2027-04-03"})
		if err != nil {
			t.Fatal(err)
		}

		_, err = e.items.Create(ctx, "member", tripID, itinerary.ItemCreate{ItemFields: itinerary.ItemFields{
			DayID: &other.Entity.ID, Title: str("Sneaky"), Category: str("OTHER"),
		}})

		if !invalidFields(t, err)["dayId"] {
			t.Error("an item must not attach to a day of another trip")
		}
	})

	t.Run("viewers cannot create", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")

		_, err := e.items.Create(ctx, "viewer", tripID, itinerary.ItemCreate{ItemFields: itinerary.ItemFields{DayID: &d.ID, Title: str("x"), Category: str("OTHER")}})

		requireApp(t, err, apperror.KindForbidden, "forbidden")
	})
}

func TestUpdateItem(t *testing.T) {
	ctx := context.Background()
	patch := func(version int64, f itinerary.ItemFields) itinerary.ItemPatch {
		p := itinerary.ItemPatch{ItemFields: f}
		p.SetVersion(version)
		return p
	}

	t.Run("partial update keeps the rest and bumps the version", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")
		item := e.item(t, d.ID, "Old", func(in *itinerary.ItemCreate) { in.Notes = str("keep me"); in.Start = zt("2027-04-03T09:00") })

		res, err := e.items.Update(ctx, "member", tripID, item.ID, patch(1, itinerary.ItemFields{Title: str("New"), Status: str("CONFIRMED")}))

		if err != nil || res.Entity.Title != "New" || res.Entity.Status != kernel.StatusConfirmed || res.Entity.Notes != "keep me" || res.Entity.Start == nil || res.Entity.Version != 2 {
			t.Errorf("Update() = %+v, %v", res.Entity, err)
		}
	})

	t.Run("null clears an optional field, absent leaves it", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")
		item := e.item(t, d.ID, "Timed", func(in *itinerary.ItemCreate) { in.Start = zt("2027-04-03T09:00") })

		res, err := e.items.Update(ctx, "member", tripID, item.ID, patch(1, itinerary.ItemFields{Start: clear[kernel.ZonedTimeInput]()}))

		if err != nil || res.Entity.Start != nil {
			t.Errorf("Update() start = %+v, %v; want it cleared", res.Entity.Start, err)
		}
	})

	t.Run("moving to another day requires the start to follow", func(t *testing.T) {
		e := newEnv()
		d1, d2 := e.day(t, "2027-04-03"), e.day(t, "2027-04-04")
		item := e.item(t, d1.ID, "Timed", func(in *itinerary.ItemCreate) { in.Start = zt("2027-04-03T09:00") })

		_, err := e.items.Update(ctx, "member", tripID, item.ID, patch(1, itinerary.ItemFields{DayID: &d2.ID}))
		if !invalidFields(t, err)["start.dateTime"] {
			t.Error("a timed item cannot move to a day its start is not on")
		}

		res, err := e.items.Update(ctx, "member", tripID, item.ID, patch(1, itinerary.ItemFields{DayID: &d2.ID, Start: zt("2027-04-04T09:00")}))
		if err != nil || res.Entity.DayID != d2.ID {
			t.Errorf("moving with the new start: %+v, %v", res.Entity, err)
		}
	})

	t.Run("an unchanged patch does not touch the version and stale versions conflict", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")
		item := e.item(t, d.ID, "Same")

		res, err := e.items.Update(ctx, "member", tripID, item.ID, patch(1, itinerary.ItemFields{Title: str("Same")}))
		if err != nil || res.Entity.Version != 1 {
			t.Errorf("no-op update version = %d, %v", res.Entity.Version, err)
		}
		if _, err := e.items.Update(ctx, "member", tripID, item.ID, patch(1, itinerary.ItemFields{Title: str("A")})); err != nil {
			t.Fatal(err)
		}
		_, err = e.items.Update(ctx, "member", tripID, item.ID, patch(1, itinerary.ItemFields{Title: str("B")}))
		requireApp(t, err, apperror.KindConflict, "version_conflict")
	})

	t.Run("viewers cannot update", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")
		item := e.item(t, d.ID, "x")

		_, err := e.items.Update(ctx, "viewer", tripID, item.ID, patch(1, itinerary.ItemFields{Title: str("y")}))

		requireApp(t, err, apperror.KindForbidden, "forbidden")
	})
}

type staticSource []kernel.TimelineEntry

func (s staticSource) Entries(context.Context, string) ([]kernel.TimelineEntry, error) { return s, nil }

func TestTimeline(t *testing.T) {
	ctx := context.Background()

	t.Run("orders entries by real time, timed before untimed", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")
		e.item(t, d.ID, "Untimed B")
		e.item(t, d.ID, "Lunch", func(in *itinerary.ItemCreate) { in.Start = zt("2027-04-03T12:00") })
		e.item(t, d.ID, "Breakfast", func(in *itinerary.ItemCreate) { in.Start = zt("2027-04-03T08:00") })
		e.item(t, d.ID, "Untimed A")

		got, role, err := e.timeline.Get(ctx, "viewer", tripID)
		if err != nil || role != trip.RoleViewer {
			t.Fatalf("Get() error = %v, role %s", err, role)
		}

		var titles []string
		for _, entry := range got.Days[0].Entries {
			titles = append(titles, entry.Title)
		}
		want := []string{"Breakfast", "Lunch", "Untimed B", "Untimed A"}
		if len(titles) != 4 || titles[0] != want[0] || titles[1] != want[1] || titles[2] != want[2] || titles[3] != want[3] {
			t.Errorf("order = %v, want %v (untimed keep their manual order)", titles, want)
		}
	})

	t.Run("compares events in different zones by instant", func(t *testing.T) {
		zone := func(dt, tz string) *kernel.ZonedTime {
			z, err := kernel.ParseZonedTime(dt, kernel.Timezone(tz))
			if err != nil {
				t.Fatal(err)
			}
			return &z
		}
		// 22:00 in Sao Paulo (01:00Z next day) happens after 23:00 in Lisbon (22:00Z).
		e := newEnv(staticSource{
			{Kind: "flight", ID: "b", Title: "Sao Paulo evening", Day: "2027-04-03", Start: zone("2027-04-03T22:00", "America/Sao_Paulo")},
			{Kind: "flight", ID: "a", Title: "Lisbon night", Day: "2027-04-03", Start: zone("2027-04-03T23:00", "Europe/Lisbon")},
		})

		got, _, err := e.timeline.Get(ctx, "member", tripID)

		if err != nil || got.Days[0].Entries[0].Title != "Lisbon night" || got.Days[0].Entries[1].Title != "Sao Paulo evening" {
			t.Errorf("entries = %+v, want them ordered by instant, not by wall clock", got.Days[0].Entries)
		}
	})

	t.Run("other domains add dates that have no day record", func(t *testing.T) {
		e := newEnv(staticSource{{Kind: "flight", ID: "f1", Title: "Arrival", Day: "2027-04-01"}})
		e.day(t, "2027-04-03")

		got, _, err := e.timeline.Get(ctx, "member", tripID)

		if err != nil || len(got.Days) != 2 || got.Days[0].Date != "2027-04-01" || got.Days[0].Day != nil || got.Days[1].Day == nil {
			t.Errorf("days = %+v, want an entry-only date before the real day", got.Days)
		}
	})

	t.Run("deleted items disappear and access is enforced", func(t *testing.T) {
		e := newEnv()
		d := e.day(t, "2027-04-03")
		item := e.item(t, d.ID, "Gone soon")
		if err := e.items.Resource().Delete(ctx, "member", tripID, item.ID, nil); err != nil {
			t.Fatal(err)
		}

		got, _, _ := e.timeline.Get(ctx, "owner", tripID)
		if len(got.Days[0].Entries) != 0 {
			t.Errorf("entries = %+v, want none", got.Days[0].Entries)
		}
		_, _, err := e.timeline.Get(ctx, "stranger", tripID)
		requireApp(t, err, apperror.KindNotFound, "trip_not_found")
	})
}
