// Package quotatest holds an in-memory quota.Counter for tests.
package quotatest

import (
	"context"
	"sync"

	"rinotravel-api/internal/quota"
)

// Counter counts in memory with the same semantics as the Mongo counter. Fail, when set, is
// returned by every Take, to exercise the "counter is broken" path.
type Counter struct {
	mu     sync.Mutex
	counts map[string]int
	Fail   error
}

func (c *Counter) Take(_ context.Context, bucket string, limit int) (int, error) {
	if c.Fail != nil {
		return 0, c.Fail
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	if c.counts[bucket] >= limit {
		return c.counts[bucket], quota.ErrExhausted
	}
	c.counts[bucket]++
	return c.counts[bucket], nil
}

// Used reports how many calls a bucket has taken.
func (c *Counter) Used(bucket string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[bucket]
}

// Set pre-fills a bucket, for tests that start with the allowance already (almost) used.
func (c *Counter) Set(bucket string, used int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	c.counts[bucket] = used
}
