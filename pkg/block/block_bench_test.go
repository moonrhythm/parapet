package block_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/block"
)

// Block is the conditional container behind host/location matchers — its
// per-request cost is paid by every matcher in the framework. The benchmark
// covers both branches (match → inner chain, miss → wrapped handler) since
// real configs hit both paths every request.

// noopMW replaces the block's default inner terminal (http.NotFoundHandler)
// so the match-path benchmark isolates dispatch cost, not 404 generation.
var noopMW = parapet.MiddlewareFunc(func(_ http.Handler) http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
})

func benchBlock(b *testing.B, match func(*http.Request) bool) {
	bl := block.New(match)
	bl.Use(noopMW)
	h := bl.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := newBenchRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

func BenchmarkMatch(b *testing.B) {
	benchBlock(b, func(*http.Request) bool { return true })
}

func BenchmarkMiss(b *testing.B) {
	benchBlock(b, func(*http.Request) bool { return false })
}

// BenchmarkNilMatch covers the unconditional-pass shortcut (block.New(nil)),
// used by host.New("*") and similar always-match constructors.
func BenchmarkNilMatch(b *testing.B) {
	bl := block.New(nil)
	bl.Use(noopMW)
	h := bl.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := newBenchRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

type benchRW struct{ h http.Header }

func newBenchRW() *benchRW                          { return &benchRW{h: make(http.Header)} }
func (w *benchRW) Header() http.Header              { return w.h }
func (w *benchRW) Write(p []byte) (int, error)      { return io.Discard.Write(p) }
func (w *benchRW) WriteHeader(int)                  {}
