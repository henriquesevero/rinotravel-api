package clock

import "time"

// MongoDB stores dates with millisecond precision. Truncating at creation keeps
// the value returned to the caller identical to the value read back later.
func Now() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}
