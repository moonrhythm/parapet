package ratelimit_test

import (
	"net/http"
	"time"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/ratelimit"
)

// Cap each client IP to a fixed number of requests per window. This is the most
// common setup: callers exceeding the budget get a 429 with a Retry-After header.
// The default Key is ClientIP, so no further configuration is needed.
func ExampleFixedWindowPerSecond() {
	s := parapet.New()
	s.Use(ratelimit.FixedWindowPerSecond(10)) // 10 req/s per client IP
	// s.Use(upstream.SingleHost("10.0.0.1:8080")) — the protected backend.
}

// FixedWindow with an arbitrary window size when the per-second/minute/hour
// helpers don't fit — here, 100 requests every 5 minutes.
func ExampleFixedWindow() {
	s := parapet.New()
	s.Use(ratelimit.FixedWindow(100, 5*time.Minute))
}

// Concurrent caps the number of in-flight requests per key rather than the
// arrival rate; once a request finishes, its slot is freed. Excess requests are
// rejected immediately. Useful for protecting an expensive endpoint from pile-up.
func ExampleConcurrent() {
	s := parapet.New()
	s.Use(ratelimit.Concurrent(4)) // at most 4 in-flight requests per client IP
}

// ConcurrentQueue is like Concurrent but holds excess requests in a bounded
// queue instead of rejecting them outright: up to Capacity run at once, up to
// Size wait, anything beyond that is dropped.
func ExampleConcurrentQueue() {
	s := parapet.New()
	s.Use(ratelimit.ConcurrentQueue(4, 16)) // 4 concurrent, 16 queued per key
}

// LeakyBucket smooths bursts by spacing requests out: it admits one request per
// PerRequest interval, buffering up to Size before dropping. Here, ~5 req/s with
// a 10-deep buffer.
func ExampleLeakyBucket() {
	s := parapet.New()
	s.Use(ratelimit.LeakyBucket(200*time.Millisecond, 10))
}

// Rate-limit on something other than the client IP by setting Key — here, an API
// key header, so the limit is per credential regardless of source address.
func ExampleNew() {
	m := ratelimit.New(&ratelimit.FixedWindowStrategy{
		Max:  60,
		Size: time.Minute,
	})
	m.Key = func(r *http.Request) string {
		return r.Header.Get("X-API-Key")
	}

	s := parapet.New()
	s.Use(m)
}

// Customize the over-limit response with ExceededHandler instead of the default
// plain-text 429.
func ExampleRateLimiter_exceededHandler() {
	m := ratelimit.FixedWindowPerMinute(120)
	m.ExceededHandler = func(w http.ResponseWriter, r *http.Request, after time.Duration) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}

	s := parapet.New()
	s.Use(m)
}
