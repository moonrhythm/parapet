package host_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/host"
)

// Host matching has three shapes worth measuring:
//   1. exact match against the hostMap (one map lookup)
//   2. wildcard match (one map lookup + a strings.Index loop walking
//      subdomain boundaries — cost grows with subdomain depth)
//   3. miss (worst case: walks the entire subdomain chain before returning)

func benchHost(b *testing.B, hosts []string, reqHost string) {
	m := host.New(hosts...)
	// Replace the block's default NotFoundHandler inner terminal with a no-op
	// so matched-path benchmarks measure host matching, not 404 generation.
	m.Use(parapet.MiddlewareFunc(func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}))
	h := m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = reqHost
	w := newBenchRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

func BenchmarkExactMatch(b *testing.B) {
	benchHost(b, []string{"api.example.com"}, "api.example.com")
}

func BenchmarkWildcardShallow(b *testing.B) {
	// One subdomain level — the loop hits the wildcard on the first iteration.
	benchHost(b, []string{"*.example.com"}, "api.example.com")
}

func BenchmarkWildcardDeep(b *testing.B) {
	// Several subdomain levels — the loop walks down before matching the wildcard.
	benchHost(b, []string{"*.example.com"}, "a.b.c.d.example.com")
}

func BenchmarkMiss(b *testing.B) {
	// Worst case: walks the full subdomain chain, never matches.
	benchHost(b, []string{"*.example.com"}, "a.b.c.d.other.org")
}

type benchRW struct{ h http.Header }

func newBenchRW() *benchRW                          { return &benchRW{h: make(http.Header)} }
func (w *benchRW) Header() http.Header              { return w.h }
func (w *benchRW) Write(p []byte) (int, error)      { return io.Discard.Write(p) }
func (w *benchRW) WriteHeader(int)                  {}
