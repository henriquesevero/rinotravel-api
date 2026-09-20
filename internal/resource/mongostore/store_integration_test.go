//go:build integration

package mongostore_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rinotravel-api/internal/kernel"
	"rinotravel-api/internal/platform/ids"
	"rinotravel-api/internal/platform/mongodb"
	"rinotravel-api/internal/platform/mongodb/mongotest"
	"rinotravel-api/internal/resource/mongostore"
)

type note struct {
	kernel.Base
	Text string
	Slot string
}

var codec = mongostore.Codec[note]{
	Base:   func(n *note) *kernel.Base { return &n.Base },
	Encode: func(n note) bson.D { return bson.D{{Key: "text", Value: n.Text}, {Key: "slot", Value: n.Slot}} },
	Decode: func(raw bson.Raw, base kernel.Base) (note, error) {
		var doc struct {
			Text string `bson:"text"`
			Slot string `bson:"slot"`
		}
		err := bson.Unmarshal(raw, &doc)
		return note{Base: base, Text: doc.Text, Slot: doc.Slot}, err
	},
}

var now = time.Now().UTC().Truncate(time.Millisecond)

func setup(t *testing.T) (*mongostore.Store[note], *mongo.Database) {
	t.Helper()
	db := mongotest.Database(t)
	store := mongostore.New(db, "notes", codec)
	if err := store.EnsureIndexes(context.Background(), mongostore.UniqueAmongLive(bson.E{Key: "slot", Value: 1})); err != nil {
		t.Fatal(err)
	}
	return store, db
}

func newNote(trip, slot string) note {
	return note{Base: kernel.NewBase(ids.New(), trip, now), Text: "hello", Slot: slot}
}

func seqOf(t *testing.T, db *mongo.Database, id string) int64 {
	t.Helper()
	var doc struct {
		Seq int64 `bson:"seq"`
	}
	if err := db.Collection("notes").FindOne(context.Background(), bson.D{{Key: "_id", Value: id}}).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	return doc.Seq
}

func TestStore_RoundTripAndTripScoping(t *testing.T) {
	ctx := context.Background()
	store, _ := setup(t)
	n := newNote("trip-1", "a")

	if err := store.Insert(ctx, n); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	got, err := store.Get(ctx, "trip-1", n.ID)
	if err != nil || got != n {
		t.Errorf("Get() = %+v, %v; want %+v", got, err, n)
	}
	if _, err := store.Get(ctx, "trip-2", n.ID); !errors.Is(err, kernel.ErrNotFound) {
		t.Errorf("Get() from another trip error = %v, want ErrNotFound", err)
	}
	if err := store.Insert(ctx, n); !errors.Is(err, kernel.ErrAlreadyExists) {
		t.Errorf("duplicate Insert() error = %v, want ErrAlreadyExists", err)
	}
}

func TestStore_ReplaceChecksVersionAndStampsSeq(t *testing.T) {
	ctx := context.Background()
	store, db := setup(t)
	n := newNote("trip-1", "a")
	if err := store.Insert(ctx, n); err != nil {
		t.Fatal(err)
	}
	if seq := seqOf(t, db, n.ID); seq != 1 {
		t.Fatalf("seq after insert = %d, want 1", seq)
	}

	updated := n
	updated.Text = "changed"
	updated.Touch(now.Add(time.Hour))
	if err := store.Replace(ctx, updated); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	if seq := seqOf(t, db, n.ID); seq != 2 {
		t.Errorf("seq after replace = %d, want 2", seq)
	}

	stale := n
	stale.Text = "stale"
	stale.Touch(now.Add(2 * time.Hour))
	if err := store.Replace(ctx, stale); !errors.Is(err, kernel.ErrVersionConflict) {
		t.Errorf("stale Replace() error = %v, want ErrVersionConflict", err)
	}
	missing := newNote("trip-1", "z")
	missing.Version = 2
	if err := store.Replace(ctx, missing); !errors.Is(err, kernel.ErrNotFound) {
		t.Errorf("Replace() of a missing entity error = %v, want ErrNotFound", err)
	}
}

func TestStore_TombstonesHideEntitiesButStayVisibleToSync(t *testing.T) {
	ctx := context.Background()
	store, db := setup(t)
	n := newNote("trip-1", "a")
	if err := store.Insert(ctx, n); err != nil {
		t.Fatal(err)
	}

	gone := n
	gone.MarkDeleted(now.Add(time.Hour))
	if err := store.Replace(ctx, gone); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Get(ctx, "trip-1", n.ID); !errors.Is(err, kernel.ErrNotFound) {
		t.Errorf("Get() after delete error = %v, want ErrNotFound", err)
	}
	if list, _ := store.List(ctx, "trip-1"); len(list) != 0 {
		t.Errorf("List() = %d, want none", len(list))
	}
	tomb, err := store.GetAny(ctx, "trip-1", n.ID)
	if err != nil || !tomb.IsDeleted() || tomb.Version != 2 {
		t.Errorf("GetAny() = %+v, %v", tomb.Base, err)
	}
	again := gone
	again.Touch(now.Add(2 * time.Hour))
	if err := store.Replace(ctx, again); !errors.Is(err, kernel.ErrNotFound) {
		t.Errorf("updating a tombstone error = %v, want ErrNotFound", err)
	}

	var raw struct {
		PurgeAt time.Time `bson:"purgeAt"`
	}
	if err := db.Collection("notes").FindOne(ctx, bson.D{{Key: "_id", Value: n.ID}}).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if want := gone.DeletedAt.Add(mongostore.TombstoneRetention); raw.PurgeAt.Sub(want).Abs() > time.Second {
		t.Errorf("purgeAt = %v, want deletedAt + 90 days (%v)", raw.PurgeAt, want)
	}
}

func TestStore_ChangesAreOrderedBySeqAndScopedToTheTrip(t *testing.T) {
	ctx := context.Background()
	store, _ := setup(t)
	a, b, other := newNote("trip-1", "a"), newNote("trip-1", "b"), newNote("trip-2", "a")
	for _, n := range []note{a, b, other} {
		if err := store.Insert(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	gone := a
	gone.MarkDeleted(now.Add(time.Hour))
	if err := store.Replace(ctx, gone); err != nil {
		t.Fatal(err)
	}

	initial, err := store.Changes(ctx, "trip-1", 0, 100)
	if err != nil || len(initial) != 1 || initial[0].Entity.ID != b.ID {
		t.Fatalf("initial Changes() = %+v, %v; want only the live entity of the trip", initial, err)
	}
	after, _ := store.Changes(ctx, "trip-1", initial[0].Seq, 100)
	if len(after) != 1 || after[0].Entity.ID != a.ID || !after[0].Entity.IsDeleted() {
		t.Errorf("incremental Changes() = %+v, want the tombstone", after)
	}
	limited, _ := store.Changes(ctx, "trip-1", 0, 1)
	if len(limited) != 1 {
		t.Errorf("limit not applied: %d", len(limited))
	}
}

func TestStore_UniqueIndexAppliesOnlyToLiveEntities(t *testing.T) {
	ctx := context.Background()
	store, _ := setup(t)
	first := newNote("trip-1", "day-1")
	if err := store.Insert(ctx, first); err != nil {
		t.Fatal(err)
	}

	if err := store.Insert(ctx, newNote("trip-1", "day-1")); !errors.Is(err, kernel.ErrAlreadyExists) {
		t.Errorf("clashing Insert() error = %v, want ErrAlreadyExists", err)
	}
	if err := store.Insert(ctx, newNote("trip-2", "day-1")); err != nil {
		t.Errorf("the same slot in another trip must be fine: %v", err)
	}

	gone := first
	gone.MarkDeleted(now.Add(time.Hour))
	if err := store.Replace(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(ctx, newNote("trip-1", "day-1")); err != nil {
		t.Errorf("a deleted entity must free its slot: %v", err)
	}
}

func TestStore_ConcurrentReplacesHaveASingleWinner(t *testing.T) {
	ctx := context.Background()
	store, db := setup(t)
	n := newNote("trip-1", "a")
	if err := store.Insert(ctx, n); err != nil {
		t.Fatal(err)
	}

	const writers = 10
	errs := make([]error, writers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := n
			c.Text = "writer"
			c.Touch(now.Add(time.Hour))
			<-start
			errs[i] = store.Replace(ctx, c)
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for _, err := range errs {
		switch {
		case err == nil:
			winners++
		case !errors.Is(err, kernel.ErrVersionConflict):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if winners != 1 {
		t.Errorf("%d writers succeeded, want exactly 1", winners)
	}
	if seq := seqOf(t, db, n.ID); seq != 2 {
		t.Errorf("seq = %d, want 2: losers must roll back their allocated seq", seq)
	}
}

func TestStore_WritesJoinAnOuterTransaction(t *testing.T) {
	ctx := context.Background()
	store, db := setup(t)
	boom := errors.New("boom")
	a, b := newNote("trip-1", "a"), newNote("trip-1", "b")

	err := mongodb.WithTransaction(ctx, db, func(ctx context.Context) error {
		if err := store.Insert(ctx, a); err != nil {
			return err
		}
		if err := store.Insert(ctx, b); err != nil {
			return err
		}
		return boom
	})

	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the callback error", err)
	}
	if list, _ := store.List(ctx, "trip-1"); len(list) != 0 {
		t.Errorf("%d entities survived a rolled back outer transaction", len(list))
	}

	err = mongodb.WithTransaction(ctx, db, func(ctx context.Context) error {
		if err := store.Insert(ctx, a); err != nil {
			return err
		}
		return store.Insert(ctx, b)
	})
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := store.List(ctx, "trip-1"); len(list) != 2 {
		t.Errorf("committed outer transaction stored %d entities, want 2", len(list))
	}
}
