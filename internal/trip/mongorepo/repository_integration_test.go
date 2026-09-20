//go:build integration

package mongorepo_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/platform/mongodb/mongotest"
	"rinotravel-api/internal/trip"
	"rinotravel-api/internal/trip/mongorepo"
	"rinotravel-api/internal/user"
)

var now = time.Now().UTC().Truncate(time.Millisecond)

func setup(t *testing.T) (*mongorepo.Repository, *mongo.Database) {
	t.Helper()
	db := mongotest.Database(t)
	repo := mongorepo.New(db)
	if err := repo.EnsureIndexes(context.Background()); err != nil {
		t.Fatalf("EnsureIndexes() error = %v", err)
	}
	return repo, db
}

func newTrip(owner user.ID, startDate string) trip.Trip {
	return trip.New(trip.ID(ids.New()), owner, trip.Details{
		Name:        "Japan 2027",
		Destination: "Tokyo",
		StartDate:   kernel.Date(startDate),
		EndDate:     "2027-12-31",
		Timezone:    "Asia/Tokyo",
		Currency:    "JPY",
	}, now)
}

func seqOf(t *testing.T, db *mongo.Database, id trip.ID) int64 {
	t.Helper()
	var doc struct {
		Seq int64 `bson:"seq"`
	}
	if err := db.Collection("trips").FindOne(context.Background(), bson.D{{Key: "_id", Value: string(id)}}).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc.Seq
}

func counterOf(t *testing.T, db *mongo.Database, id trip.ID) int64 {
	t.Helper()
	var doc struct {
		Seq int64 `bson:"seq"`
	}
	err := db.Collection("sync_counters").FindOne(context.Background(), bson.D{{Key: "_id", Value: string(id)}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return doc.Seq
}

func TestRepository_CreateAndFindRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, _ := setup(t)
	want := newTrip("ana", "2027-04-01")
	if err := want.AddMember("bia", trip.RoleViewer, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	want.Version = 1

	if err := repo.Create(ctx, want); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := repo.FindByID(ctx, want.ID)

	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("FindByID() = %+v, %v\nwant %+v", got, err, want)
	}
}

func TestRepository_CreateRejectsDuplicateIDWithoutConsumingASeq(t *testing.T) {
	ctx := context.Background()
	repo, db := setup(t)
	original := newTrip("ana", "2027-04-01")
	if err := repo.Create(ctx, original); err != nil {
		t.Fatal(err)
	}

	intruder := newTrip("bia", "2028-01-01")
	intruder.ID = original.ID
	err := repo.Create(ctx, intruder)

	if !errors.Is(err, trip.ErrAlreadyExists) {
		t.Fatalf("Create() error = %v, want ErrAlreadyExists", err)
	}
	if got, _ := repo.FindByID(ctx, original.ID); got.OwnerID() != "ana" {
		t.Error("the existing trip was overwritten")
	}
	if seq := counterOf(t, db, original.ID); seq != 1 {
		t.Errorf("counter = %d, want 1: the failed create must roll back its seq", seq)
	}
}

func TestRepository_UpdateChecksTheVersion(t *testing.T) {
	ctx := context.Background()
	repo, _ := setup(t)
	original := newTrip("ana", "2027-04-01")
	if err := repo.Create(ctx, original); err != nil {
		t.Fatal(err)
	}

	renamed := original
	renamed.UpdateDetails(trip.Details{Name: "Renamed", Destination: "Tokyo", StartDate: "2027-04-01", EndDate: "2027-12-31", Timezone: "Asia/Tokyo", Currency: "JPY"}, now.Add(time.Hour))
	if err := repo.Update(ctx, renamed); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	stale := original
	stale.UpdateDetails(trip.Details{Name: "Stale", Destination: "Tokyo", StartDate: "2027-04-01", EndDate: "2027-12-31", Timezone: "Asia/Tokyo", Currency: "JPY"}, now.Add(2*time.Hour))
	if err := repo.Update(ctx, stale); !errors.Is(err, trip.ErrVersionConflict) {
		t.Errorf("Update() with a stale version error = %v, want ErrVersionConflict", err)
	}

	got, _ := repo.FindByID(ctx, original.ID)
	if got.Name != "Renamed" || got.Version != 2 {
		t.Errorf("stored = %q v%d, want the first update to win", got.Name, got.Version)
	}
}

func TestRepository_UpdateOfMissingTripIsNotFound(t *testing.T) {
	repo, _ := setup(t)
	missing := newTrip("ana", "2027-04-01")
	missing.Version = 2

	if err := repo.Update(context.Background(), missing); !errors.Is(err, trip.ErrNotFound) {
		t.Errorf("Update() error = %v, want ErrNotFound", err)
	}
}

func TestRepository_SoftDeleteHidesTheTripAndBlocksFurtherUpdates(t *testing.T) {
	ctx := context.Background()
	repo, db := setup(t)
	original := newTrip("ana", "2027-04-01")
	if err := repo.Create(ctx, original); err != nil {
		t.Fatal(err)
	}

	deleted := original
	deleted.MarkDeleted(now.Add(time.Hour))
	if err := repo.Update(ctx, deleted); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if _, err := repo.FindByID(ctx, original.ID); !errors.Is(err, trip.ErrNotFound) {
		t.Errorf("FindByID() after delete error = %v, want ErrNotFound", err)
	}
	if list, _ := repo.ListByMember(ctx, "ana"); len(list) != 0 {
		t.Errorf("ListByMember() = %d trips, want none", len(list))
	}
	again := deleted
	again.Version++
	if err := repo.Update(ctx, again); !errors.Is(err, trip.ErrNotFound) {
		t.Errorf("updating a deleted trip error = %v, want ErrNotFound", err)
	}

	var raw struct {
		DeletedAt *time.Time `bson:"deletedAt"`
		Version   int64      `bson:"version"`
	}
	if err := db.Collection("trips").FindOne(ctx, bson.D{{Key: "_id", Value: string(original.ID)}}).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if raw.DeletedAt == nil || raw.Version != 2 {
		t.Errorf("the tombstone was not kept: %+v", raw)
	}
}

func TestRepository_ListByMemberIsScopedAndSortedByStartDateDescending(t *testing.T) {
	ctx := context.Background()
	repo, _ := setup(t)
	early, late := newTrip("ana", "2027-01-01"), newTrip("ana", "2027-09-01")
	shared := newTrip("bia", "2027-05-01")
	if err := shared.AddMember("ana", trip.RoleViewer, now); err != nil {
		t.Fatal(err)
	}
	someoneElses := newTrip("caio", "2027-06-01")
	for _, tr := range []trip.Trip{early, late, shared, someoneElses} {
		if err := repo.Create(ctx, tr); err != nil {
			t.Fatal(err)
		}
	}

	got, err := repo.ListByMember(ctx, "ana")
	if err != nil {
		t.Fatal(err)
	}

	want := []trip.ID{late.ID, shared.ID, early.ID}
	var ids []trip.ID
	for _, tr := range got {
		ids = append(ids, tr.ID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ListByMember() = %v, want %v", ids, want)
	}
}

func TestRepository_SeqIncreasesWithEveryWrite(t *testing.T) {
	ctx := context.Background()
	repo, db := setup(t)
	current := newTrip("ana", "2027-04-01")
	if err := repo.Create(ctx, current); err != nil {
		t.Fatal(err)
	}
	if seq := seqOf(t, db, current.ID); seq != 1 {
		t.Fatalf("seq after create = %d, want 1", seq)
	}

	for want := int64(2); want <= 4; want++ {
		if err := current.AddMember(user.ID(ids.New()), trip.RoleViewer, now); err != nil {
			t.Fatal(err)
		}
		if err := repo.Update(ctx, current); err != nil {
			t.Fatal(err)
		}
		if seq := seqOf(t, db, current.ID); seq != want {
			t.Errorf("seq after write = %d, want %d", seq, want)
		}
	}
}

func TestRepository_ConcurrentUpdatesFromTheSameVersionHaveASingleWinner(t *testing.T) {
	ctx := context.Background()
	repo, db := setup(t)
	original := newTrip("ana", "2027-04-01")
	if err := repo.Create(ctx, original); err != nil {
		t.Fatal(err)
	}

	const writers = 12
	var wg sync.WaitGroup
	errs := make([]error, writers)
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidate := original
			candidate.UpdateDetails(trip.Details{Name: "writer", Destination: "Tokyo", StartDate: "2027-04-01", EndDate: "2027-12-31", Timezone: "Asia/Tokyo", Currency: "JPY"}, now.Add(time.Hour))
			<-start
			errs[i] = repo.Update(ctx, candidate)
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for _, err := range errs {
		switch {
		case err == nil:
			winners++
		case !errors.Is(err, trip.ErrVersionConflict):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if winners != 1 {
		t.Errorf("%d writers succeeded, want exactly 1", winners)
	}
	if seq := seqOf(t, db, original.ID); seq != 2 {
		t.Errorf("seq = %d, want 2 (create + the single winning update)", seq)
	}
	if counter := counterOf(t, db, original.ID); counter != 2 {
		t.Errorf("counter = %d, want 2: the losers must roll back the seqs they allocated", counter)
	}
}

func TestRepository_EnsureIndexesIsIdempotent(t *testing.T) {
	repo, _ := setup(t)

	if err := repo.EnsureIndexes(context.Background()); err != nil {
		t.Errorf("second EnsureIndexes() error = %v", err)
	}
}
