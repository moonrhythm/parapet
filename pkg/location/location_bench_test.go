package location_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/block"
	"github.com/moonrhythm/parapet/pkg/location"
)

// Exact, Prefix, and RegExp build a block that conditionally enters its
// inner chain. The benchmark exercises both branches (match → inner chain,
// miss → wrapped handler) with no-op terminals so the result reflects matcher
// + block-dispatch cost rather than whatever the terminal handler does.

// noopInner replaces the block's default inner terminal (http.NotFoundHandler)
// with a no-op middleware that consumes the request without writing.
// Without this the "match" benchmarks would measure the cost of generating a
// 404 response, not the matcher itself.
func noopInner(bl *block.Block) {
	bl.Use(parapet.MiddlewareFunc(func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}))
}

func benchMatcher(b *testing.B, bl *block.Block, path string) {
	noopInner(bl)
	h := bl.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, path, nil)
	w := newBenchRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

func BenchmarkExactMatch(b *testing.B) {
	benchMatcher(b, location.Exact("/api/users"), "/api/users")
}

func BenchmarkExactMiss(b *testing.B) {
	benchMatcher(b, location.Exact("/api/users"), "/api/orders")
}

func BenchmarkPrefixMatch(b *testing.B) {
	benchMatcher(b, location.Prefix("/api/"), "/api/users/42")
}

func BenchmarkPrefixMiss(b *testing.B) {
	benchMatcher(b, location.Prefix("/api/"), "/static/logo.png")
}

// BenchmarkRegExpMatch is the worst-case matcher: regex evaluation is at least
// an order of magnitude slower than the others. Use this to justify preferring
// Prefix/Exact when the routing rule is expressible without regex.
func BenchmarkRegExpMatch(b *testing.B) {
	benchMatcher(b, location.RegExp(`^/api/v[0-9]+/users/[0-9]+$`), "/api/v1/users/42")
}

func BenchmarkRegExpMiss(b *testing.B) {
	benchMatcher(b, location.RegExp(`^/api/v[0-9]+/users/[0-9]+$`), "/static/logo.png")
}

type benchRW struct{ h http.Header }

func newBenchRW() *benchRW                          { return &benchRW{h: make(http.Header)} }
func (w *benchRW) Header() http.Header              { return w.h }
func (w *benchRW) Write(p []byte) (int, error)      { return io.Discard.Write(p) }
func (w *benchRW) WriteHeader(int)                  {}
