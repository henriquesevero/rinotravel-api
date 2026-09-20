//go:build integration

package mongodb_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rinotravel-api/internal/platform/mongodb"
	"rinotravel-api/internal/platform/mongodb/mongotest"
)

func TestNextSeq_IsGaplessAndUniqueUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	db := mongotest.Database(t)

	const writers = 25
	seqs := make([]int64, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := mongodb.WithTransaction(ctx, db, func(ctx context.Context) error {
				seq, err := mongodb.NextSeq(ctx, db, "trip-1")
				seqs[i] = seq
				return err
			})
			if err != nil {
				t.Errorf("writer %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	sort.Slice(seqs, func(a, b int) bool { return seqs[a] < seqs[b] })
	for i, seq := range seqs {
		if seq != int64(i+1) {
			t.Fatalf("seqs = %v, want 1..%d with no gaps or duplicates", seqs, writers)
		}
	}
}

func TestNextSeq_CountersArePerTrip(t *testing.T) {
	ctx := context.Background()
	db := mongotest.Database(t)

	for _, want := range []int64{1, 2} {
		if got, err := mongodb.NextSeq(ctx, db, "trip-a"); err != nil || got != want {
			t.Fatalf("NextSeq(a) = %d, %v; want %d", got, err, want)
		}
	}
	if got, err := mongodb.NextSeq(ctx, db, "trip-b"); err != nil || got != 1 {
		t.Errorf("NextSeq(b) = %d, %v; want its own counter starting at 1", got, err)
	}
}

func TestWithTransaction_RollsBackWhenTheCallbackFails(t *testing.T) {
	ctx := context.Background()
	db := mongotest.Database(t)
	boom := errors.New("boom")

	err := mongodb.WithTransaction(ctx, db, func(ctx context.Context) error {
		if _, err := mongodb.NextSeq(ctx, db, "trip-1"); err != nil {
			return err
		}
		if _, err := db.Collection("things").InsertOne(ctx, bson.D{{Key: "_id", Value: "x"}}); err != nil {
			return err
		}
		return boom
	})

	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the callback error", err)
	}
	if n, _ := db.Collection("things").CountDocuments(ctx, bson.D{}); n != 0 {
		t.Errorf("%d documents survived the rollback", n)
	}
	if got, _ := mongodb.NextSeq(ctx, db, "trip-1"); got != 1 {
		t.Errorf("seq after a rolled back transaction = %d, want 1", got)
	}
}
