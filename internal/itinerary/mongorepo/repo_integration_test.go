//go:build integration

package mongorepo_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"rinotravel-api/internal/itinerary"
	"rinotravel-api/internal/itinerary/mongorepo"
	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/platform/mongodb/mongotest"
)

var now = time.Now().UTC().Truncate(time.Millisecond)

func TestItemRoundTripKeepsEveryField(t *testing.T) {
	ctx := context.Background()
	db := mongotest.Database(t)
	store := mongorepo.NewItemStore(db)
	if err := store.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	start, _ := kernel.ParseZonedTime("2027-04-03T09:30", "Asia/Tokyo")
	end, _ := kernel.ParseZonedTime("2027-04-03T11:00", "Asia/Tokyo")
	minutes := 45
	want := itinerary.Item{
		Base: kernel.NewBase(ids.New(), "trip-1", now), DayID: "day-1", Title: "Senso-ji", Description: "Temple",
		Category: itinerary.CategoryAttraction, Status: kernel.StatusConfirmed, Notes: "arrive early", PlaceID: "place-1", Position: 3,
		Start: &start, End: &end, DurationMinutes: &minutes,
		Location: kernel.Location{Name: "Senso-ji", Address: "Asakusa", Coordinates: &kernel.Coordinates{Lat: 35.7148, Lng: 139.7967}},
		Cost:     &kernel.Money{Amount: 500, Currency: "JPY"},
	}

	if err := store.Insert(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "trip-1", want.ID)

	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v\nerr %v", got, want, err)
	}
}

func TestMinimalItemAndClearedFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := mongorepo.NewItemStore(mongotest.Database(t))
	want := itinerary.Item{Base: kernel.NewBase(ids.New(), "trip-1", now), DayID: "d", Title: "Bare", Category: itinerary.CategoryOther, Status: kernel.StatusPlanned}

	if err := store.Insert(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, "trip-1", want.ID)

	if got.Start != nil || got.End != nil || got.Cost != nil || got.DurationMinutes != nil || !got.Location.IsZero() {
		t.Errorf("optional fields must stay empty: %+v", got)
	}
}

func TestOneLiveDayPerDate(t *testing.T) {
	ctx := context.Background()
	store := mongorepo.NewDayStore(mongotest.Database(t))
	if err := store.EnsureIndexes(ctx, mongorepo.DayIndexes()...); err != nil {
		t.Fatal(err)
	}
	mk := func(trip string) itinerary.Day {
		return itinerary.Day{Base: kernel.NewBase(ids.New(), trip, now), Date: "2027-04-03"}
	}

	first := mk("trip-1")
	if err := store.Insert(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(ctx, mk("trip-1")); !errors.Is(err, kernel.ErrAlreadyExists) {
		t.Errorf("second day for the same date error = %v, want ErrAlreadyExists", err)
	}
	if err := store.Insert(ctx, mk("trip-2")); err != nil {
		t.Errorf("the same date in another trip: %v", err)
	}
	gone := first
	gone.MarkDeleted(now.Add(time.Hour))
	if err := store.Replace(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(ctx, mk("trip-1")); err != nil {
		t.Errorf("a deleted day must free its date: %v", err)
	}
}
