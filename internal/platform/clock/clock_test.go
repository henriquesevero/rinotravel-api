package clock_test

import (
	"testing"
	"time"

	"rinotravel-api/internal/platform/clock"
)

func TestNow_IsUTCWithMillisecondPrecision(t *testing.T) {
	before := time.Now()
	got := clock.Now()

	if got.Location() != time.UTC {
		t.Errorf("Now() location = %v, want UTC", got.Location())
	}
	if !got.Equal(got.Truncate(time.Millisecond)) {
		t.Errorf("Now() = %v, has sub-millisecond precision", got)
	}
	if got.Before(before.Truncate(time.Millisecond)) || got.After(time.Now()) {
		t.Errorf("Now() = %v, not close to the current time", got)
	}
}
