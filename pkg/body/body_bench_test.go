package body_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moonrhythm/parapet/pkg/body"
)

// LimitRequest has two distinct paths:
//   - ContentLength known: a single compare, no allocation.
//   - Chunked (ContentLength < 0): wraps r.Body in a readCloser closure and
//     adds a context.WithCancel — these allocate per request.
//
// The benchmarks isolate both paths; the chunked path's cost is paid by every
// HTTP/1.1 chunked POST and every gRPC request through the proxy.

func BenchmarkKnownLength(b *testing.B) {
	m := body.LimitRequest(1 << 20)
	h := m.ServeHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Drain to mimic the downstream handler.
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("small body")))
	r.ContentLength = 10
	w := newBenchRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

func BenchmarkChunked(b *testing.B) {
	m := body.LimitRequest(1 << 20)
	h := m.ServeHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	w := newBenchRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Each iteration needs a fresh request: the chunked path consumes the
		// body and the readCloser wrapper isn't reusable.
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("small body")))
		r.ContentLength = -1
		h.ServeHTTP(w, r)
	}
}

type benchRW struct{ h http.Header }

func newBenchRW() *benchRW                          { return &benchRW{h: make(http.Header)} }
func (w *benchRW) Header() http.Header              { return w.h }
func (w *benchRW) Write(p []byte) (int, error)      { return io.Discard.Write(p) }
func (w *benchRW) WriteHeader(int)                  {}
