package logger_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moonrhythm/parapet/pkg/logger"
)

// Logger is the most expensive of the common middlewares: it allocates a
// record map per request, wraps the ResponseWriter, captures three
// timestamps, and JSON-encodes the record on completion. The benchmark
// pipes output to io.Discard so disk I/O doesn't dominate, and exercises
// both the bare path (no handler writes) and a realistic write path.

func BenchmarkLogger(b *testing.B) {
	m := &logger.Logger{Writer: io.Discard, OmitEmpty: true}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	w := newBenchRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

// BenchmarkLoggerWithBody includes a Write so the response body length is
// non-zero and the OmitEmpty path doesn't elide the responseBodySize field.
func BenchmarkLoggerWithBody(b *testing.B) {
	body := []byte(`{"ok":true}`)
	m := &logger.Logger{Writer: io.Discard, OmitEmpty: true}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	r.RemoteAddr = "127.0.0.1:54321"
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
