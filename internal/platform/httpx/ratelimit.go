package httpx

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"rinotravel-api/internal/apperror"
)

type RateLimiter struct {
	max    int
	window time.Duration
	key    func(*http.Request) string
	now    func() time.Time

	mu        sync.Mutex
	entries   map[string]*windowCount
	nextSweep time.Time
}

type windowCount struct {
	count   int
	resetAt time.Time
}

func NewRateLimiter(max int, window time.Duration, key func(*http.Request) string) *RateLimiter {
	return &RateLimiter{
		max:     max,
		window:  window,
		key:     key,
		now:     time.Now,
		entries: make(map[string]*windowCount),
	}
}

func (l *RateLimiter) Wrap(next HandlerFunc) HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if retryAfter, ok := l.allow(l.key(r)); !ok {
			seconds := int(math.Ceil(retryAfter.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			return apperror.TooManyRequests("rate_limited", "Too many requests. Try again later.")
		}
		return next(w, r)
	}
}

func (l *RateLimiter) allow(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	entry := l.entries[key]
	if entry == nil || !now.Before(entry.resetAt) {
		entry = &windowCount{resetAt: now.Add(l.window)}
		l.entries[key] = entry
	}
	if entry.count >= l.max {
		return entry.resetAt.Sub(now), false
	}
	entry.count++
	return 0, true
}

func (l *RateLimiter) sweep(now time.Time) {
	if now.Before(l.nextSweep) {
		return
	}
	for key, entry := range l.entries {
		if !now.Before(entry.resetAt) {
			delete(l.entries, key)
		}
	}
	l.nextSweep = now.Add(l.window)
}

// With trustProxy, only the rightmost X-Forwarded-For entry is used: it is the
// address our own proxy observed, while earlier entries are client-controlled.
func ClientIP(trustProxy bool) func(*http.Request) string {
	return func(r *http.Request) string {
		if trustProxy {
			forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
			if ip := strings.TrimSpace(forwarded[len(forwarded)-1]); ip != "" {
				return ip
			}
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		return host
	}
}
