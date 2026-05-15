package ratelimit_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moonrhythm/parapet/pkg/ratelimit"
)

// Rate limiting is a hot-path lock: every request acquires (and most release)
// a mutex. The benchmarks exercise the strategies that don't block — leaky
// bucket sleeps when the window is full, so we keep its rate generous enough
// that Take always returns immediately.

const benchKey = "client-1"

// keyFn returns a constant key so we measure the take/put cost on a single
// bucket rather than the cost of growing the per-key storage map.
func keyFn() func(*http.Request) string {
	return func(*http.Request) string { return benchKey }
}

func benchStrategy(b *testing.B, m *ratelimit.RateLimiter) {
	m.Key = keyFn()
	h := m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := newBenchRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

// BenchmarkFixedWindow exercises the per-second fixed window. Rate is huge so
// the limit is never hit and every Take succeeds.
func BenchmarkFixedWindow(b *testing.B) {
	benchStrategy(b, ratelimit.FixedWindowPerSecond(1<<30))
}

// BenchmarkConcurrent measures the concurrent strategy's Take+Put cycle —
// two mutex acquisitions per request. This is the lower bound for any
// connection-limiting middleware.
func BenchmarkConcurrent(b *testing.B) {
	benchStrategy(b, ratelimit.Concurrent(1<<30))
}

// BenchmarkLeakyBucket uses a generous rate so Take returns immediately
// without sleeping (the sleep path is correctness-tested elsewhere; here we
// measure the bookkeeping cost on the fast path).
func BenchmarkLeakyBucket(b *testing.B) {
	benchStrategy(b, ratelimit.LeakyBucket(time.Nanosecond, 1<<20))
}

// BenchmarkFixedWindowParallel measures lock contention. The fixed window
// holds a single global mutex per Take; contention should grow linearly
// with parallelism.
func BenchmarkFixedWindowParallel(b *testing.B) {
	m := ratelimit.FixedWindowPerSecond(1 << 30)
	m.Key = keyFn()
	h := m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := newBenchRW()
		for pb.Next() {
			h.ServeHTTP(w, r)
		}
	})
}

type benchRW struct{ h http.Header }

func newBenchRW() *benchRW                          { return &benchRW{h: make(http.Header)} }
func (w *benchRW) Header() http.Header              { return w.h }
func (w *benchRW) Write(p []byte) (int, error)      { return io.Discard.Write(p) }
func (w *benchRW) WriteHeader(int)                  {}
