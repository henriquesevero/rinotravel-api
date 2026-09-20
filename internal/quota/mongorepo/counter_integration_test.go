//go:build integration

package mongorepo_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rinotravel-api/internal/platform/mongodb/mongotest"
	"rinotravel-api/internal/quota"
	"rinotravel-api/internal/quota/mongorepo"
)

func TestCounter_StopsAtTheLimitAndCountsUp(t *testing.T) {
	counter := mongorepo.New(mongotest.Database(t))
	ctx := context.Background()

	for want := 1; want <= 3; want++ {
		got, err := counter.Take(ctx, "places", 3)
		if err != nil || got != want {
			t.Fatalf("Take #%d = (%d, %v), want (%d, nil)", want, got, err, want)
		}
	}
	for range 2 {
		if _, err := counter.Take(ctx, "places", 3); !errors.Is(err, quota.ErrExhausted) {
			t.Fatalf("Take past the limit err = %v, want ErrExhausted", err)
		}
	}
}

func TestCounter_BucketsAreIndependent(t *testing.T) {
	counter := mongorepo.New(mongotest.Database(t))
	ctx := context.Background()

	if _, err := counter.Take(ctx, "places", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Take(ctx, "routes", 1); err != nil {
		t.Errorf("routes bucket was affected by places: %v", err)
	}
}

func TestCounter_NeverHandsOutMoreThanTheLimitUnderConcurrency(t *testing.T) {
	counter := mongorepo.New(mongotest.Database(t))
	const limit, callers = 10, 60

	var granted, refused atomic.Int32
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch _, err := counter.Take(context.Background(), "places", limit); {
			case err == nil:
				granted.Add(1)
			case errors.Is(err, quota.ErrExhausted):
				refused.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if granted.Load() != limit || refused.Load() != callers-limit {
		t.Errorf("granted %d and refused %d, want %d and %d", granted.Load(), refused.Load(), limit, callers-limit)
	}
}

func TestCounter_StartsFreshEachMonth(t *testing.T) {
	db := mongotest.Database(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
	counter := mongorepo.NewWithClock(db, func() time.Time { return now })

	if _, err := counter.Take(ctx, "places", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Take(ctx, "places", 1); !errors.Is(err, quota.ErrExhausted) {
		t.Fatalf("second September call err = %v, want ErrExhausted", err)
	}

	now = now.Add(2 * time.Hour) // October
	if used, err := counter.Take(ctx, "places", 1); err != nil || used != 1 {
		t.Errorf("first October call = (%d, %v), want (1, nil)", used, err)
	}
}
