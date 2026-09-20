// Package quota counts calls to a paid external service so the application can stop before the
// provider's free monthly allowance runs out. Google cannot be told "never charge me" (its budgets
// only send alerts), so the ceiling has to be enforced here.
package quota

import (
	"context"
	"errors"
)

// ErrExhausted means the bucket already had `limit` calls this month; nothing was counted.
var ErrExhausted = errors.New("monthly quota exhausted")

// Counter hands out calls from a monthly allowance. Implementations must be safe for concurrent
// use by several application instances: the whole point is that two racing requests can never
// both take the last call.
type Counter interface {
	// Take reserves one call in the bucket for the current month and returns how many have been
	// taken so far, this one included. Once `limit` calls were taken it returns ErrExhausted.
	Take(ctx context.Context, bucket string, limit int) (used int, err error)
}
