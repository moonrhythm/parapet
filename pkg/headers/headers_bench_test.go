package headers_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moonrhythm/parapet/pkg/headers"
)

// Headers manipulation is among the hottest middlewares — most servers run
// at least one Set/Del on every request. The interesting axis is request vs
// response: response interceptors wrap http.ResponseWriter with interceptRW,
// which adds a stack frame and forwards Header()/WriteHeader()/Write().

func benchHeaders(b *testing.B, m interface {
	ServeHandler(http.Handler) http.Handler
}) {
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Response interceptors only fire when the inner handler writes.
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := newBenchRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

func BenchmarkSetRequest(b *testing.B) {
	benchHeaders(b, headers.SetRequest(
		"X-Forwarded-Proto", "https",
		"X-Custom", "value",
	))
}

func BenchmarkSetResponse(b *testing.B) {
	benchHeaders(b, headers.SetResponse(
		"X-Frame-Options", "DENY",
		"X-Content-Type-Options", "nosniff",
	))
}

func BenchmarkAddRequest(b *testing.B) {
	benchHeaders(b, headers.AddRequest("Via", "parapet"))
}

func BenchmarkAddResponse(b *testing.B) {
	benchHeaders(b, headers.AddResponse("Via", "parapet"))
}

func BenchmarkDeleteRequest(b *testing.B) {
	benchHeaders(b, headers.DeleteRequest("X-Forwarded-For", "X-Real-IP"))
}

func BenchmarkDeleteResponse(b *testing.B) {
	benchHeaders(b, headers.DeleteResponse("Server", "X-Powered-By"))
}

type benchRW struct{ h http.Header }

func newBenchRW() *benchRW                          { return &benchRW{h: make(http.Header)} }
func (w *benchRW) Header() http.Header              { return w.h }
func (w *benchRW) Write(p []byte) (int, error)      { return io.Discard.Write(p) }
func (w *benchRW) WriteHeader(int)                  {}
