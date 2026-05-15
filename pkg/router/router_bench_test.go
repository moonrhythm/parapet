package router_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/router"
)

func benchHandler() parapet.Middleware {
	return parapet.MiddlewareFunc(func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	})
}

// BenchmarkServeHandler measures the cost of rebuilding the underlying
// http.ServeMux. Router.ServeHandler() rebuilds the mux from scratch on every
// call — fine because it's invoked once at server start, but worth measuring
// for callers that compose routers under blocks (rebuild on every request).
func BenchmarkServeHandler(b *testing.B) {
	for _, n := range []int{1, 8, 64} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			m := router.New()
			for i := 0; i < n; i++ {
				m.Handle("/path"+strconv.Itoa(i), benchHandler())
			}
			fallback := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.ServeHandler(fallback)
			}
		})
	}
}

// BenchmarkDispatch measures per-request routing. The mux is built once;
// the loop only pays the dispatch cost. The sub-benchmarks vary the hit path
// to expose any difference between exact, longest-prefix, and fallback matches.
func BenchmarkDispatch(b *testing.B) {
	const n = 64
	m := router.New()
	for i := 0; i < n; i++ {
		m.Handle("/path"+strconv.Itoa(i), benchHandler())
	}
	fallback := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	h := m.ServeHandler(fallback)

	cases := []struct {
		name string
		path string
	}{
		{"exact-first", "/path0"},
		{"exact-last", "/path" + strconv.Itoa(n-1)},
		{"prefix", "/path0/sub/resource"},
		{"fallback", "/missing"},
	}

	w := newBenchRW()
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			r := httptest.NewRequest(http.MethodGet, c.path, nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.ServeHTTP(w, r)
			}
		})
	}
}

type benchRW struct{ h http.Header }

func newBenchRW() *benchRW                          { return &benchRW{h: make(http.Header)} }
func (w *benchRW) Header() http.Header              { return w.h }
func (w *benchRW) Write(p []byte) (int, error)      { return io.Discard.Write(p) }
func (w *benchRW) WriteHeader(int)                  {}
