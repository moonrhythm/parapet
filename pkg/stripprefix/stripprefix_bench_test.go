package stripprefix_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moonrhythm/parapet/pkg/stripprefix"
)

// StripPrefix delegates entirely to http.StripPrefix. The benchmark covers
// both branches: a matching prefix (clones the request to rewrite URL.Path)
// and a non-matching prefix (delegates to NotFoundHandler — but we don't
// exercise that here because the cost is unrepresentative).

func BenchmarkMatch(b *testing.B) {
	m := stripprefix.New("/api")
	h := m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
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
