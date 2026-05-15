package requestid_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moonrhythm/parapet/pkg/header"
	"github.com/moonrhythm/parapet/pkg/requestid"
)

// RequestID has two distinct cost profiles:
//   1. Propagate: a trusted upstream already supplied the header — just two
//      header writes (request echo + response set).
//   2. Generate: no trusted header or TrustProxy=false — a UUIDv4 is allocated
//      (reads 16 bytes from crypto/rand and formats them).

func BenchmarkPropagate(b *testing.B) {
	m := requestid.New() // TrustProxy: true
	h := m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	header.Set(r.Header, requestid.DefaultHeader, "01234567-89ab-cdef-0123-456789abcdef")
	w := newBenchRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

func BenchmarkGenerate(b *testing.B) {
	m := &requestid.RequestID{TrustProxy: false}
	h := m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
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
