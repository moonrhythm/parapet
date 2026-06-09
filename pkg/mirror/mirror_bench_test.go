package mirror_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moonrhythm/parapet/pkg/mirror"
)

// BenchmarkServeHandlerNoMiss measures the gate-only hot path: a non-matching request
// short-circuits to next with no clone/buffer/goroutine. This is what every request
// pays when it is not selected for mirroring.
func BenchmarkServeHandlerNoMiss(b *testing.B) {
	m := mirror.New()
	m.Match = func(*http.Request) bool { return false } // never mirror
	h := m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		h.ServeHTTP(w, r)
	}
}

// BenchmarkServeHandlerUnderCapBody measures the capture+dispatch cost of a small
// mirrored body. A generous queue and a discard destination keep workers from
// becoming the bottleneck so the bench reflects the request-goroutine cost.
func BenchmarkServeHandlerUnderCapBody(b *testing.B) {
	m := mirror.New()
	m.QueueSize = 1 << 16
	m.Use(parapetNoopMiddleware{})
	h := m.ServeHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = r.Body.Read(make([]byte, 0))
	}))
	body := bytes.Repeat([]byte("x"), 256)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
}

// parapetNoopMiddleware is a discard mirror destination for benchmarks.
type parapetNoopMiddleware struct{}

func (parapetNoopMiddleware) ServeHandler(http.Handler) http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
}
