package ratelimit

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// NewFixedWindow creates new fixed window rate limiter with default config
func NewFixedWindow(rate int, unit time.Duration) *FixedWindowRateLimiter {
	m := &FixedWindowRateLimiter{
		Key: func(r *http.Request) string {
			return r.Header.Get("X-Forwarded-For")
		},
		Rate: rate,
		Unit: unit,
	}
	return m
}

// FixedWindowPerSecond creates new rate limiter per second
func FixedWindowPerSecond(rate int) *FixedWindowRateLimiter {
	return NewFixedWindow(rate, time.Second)
}

// FixedWindowPerMinute creates new rate limiter per minute
func FixedWindowPerMinute(rate int) *FixedWindowRateLimiter {
	return NewFixedWindow(rate, time.Minute)
}

// FixedWindowPerHour creates new rate limiter per hour
func FixedWindowPerHour(rate int) *FixedWindowRateLimiter {
	return NewFixedWindow(rate, time.Hour)
}

// FixedWindowRateLimiter middleware
type FixedWindowRateLimiter struct {
	Key             func(r *http.Request) string
	Rate            int
	Unit            time.Duration
	ExceededHandler http.Handler

	bucket fixedWindowBucket
}

// ServeHandler implements middleware interface
func (m *FixedWindowRateLimiter) ServeHandler(h http.Handler) http.Handler {
	if m.Key == nil || m.Rate <= 0 || m.Unit <= 0 {
		return h
	}

	if m.ExceededHandler == nil {
		m.ExceededHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			now := time.Now()
			after := (now.Truncate(m.Unit).UnixNano() + int64(m.Unit) - now.UnixNano()) / int64(time.Second)
			if after <= 0 {
				after = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(after, 10))
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := time.Now().UnixNano() / int64(m.Unit)
		key := m.Key(r)

		current := m.bucket.Incr(t, key, m.Rate)
		if current > m.Rate {
			m.ExceededHandler.ServeHTTP(w, r)
			return
		}

		h.ServeHTTP(w, r)
	})
}

type fixedWindowBucket struct {
	mu sync.Mutex
	t  int64
	d  map[string]int
}

func (b *fixedWindowBucket) Incr(t int64, k string, max int) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.t != t {
		b.t = t
		if len(b.d) > 0 || b.d == nil {
			b.d = make(map[string]int)
		}
	}

	x := b.d[k] + 1
	if x <= max {
		b.d[k] = x
	}
	return x
}
