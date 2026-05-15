package parapet_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	. "github.com/moonrhythm/parapet"
)

// passthrough returns a middleware that does nothing but call the next handler.
// Use it to measure pure chain-dispatch cost without confounders.
func passthrough() Middleware {
	return MiddlewareFunc(func(next http.Handler) http.Handler {
		return next
	})
}

// wrap returns a middleware that wraps the next handler in a new closure.
// This is the realistic shape: every middleware adds a stack frame per request.
func wrap() Middleware {
	return MiddlewareFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	})
}

var noopHandler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

// BenchmarkMiddlewaresServeHandler measures the one-time cost of composing
// a chain at server start. The chain length sweep shows the per-middleware
// overhead in the composition path.
func BenchmarkMiddlewaresServeHandler(b *testing.B) {
	for _, n := range []int{1, 4, 16, 64} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			var ms Middlewares
			for i := 0; i < n; i++ {
				ms.Use(wrap())
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = ms.ServeHandler(noopHandler)
			}
		})
	}
}

// BenchmarkChainDispatch measures the per-request cost of dispatching
// through a composed chain. Each middleware adds one closure call.
func BenchmarkChainDispatch(b *testing.B) {
	for _, n := range []int{1, 4, 16, 64} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			var ms Middlewares
			for i := 0; i < n; i++ {
				ms.Use(wrap())
			}
			h := ms.ServeHandler(noopHandler)
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			w := newDiscardResponseWriter()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.ServeHTTP(w, r)
			}
		})
	}
}

// BenchmarkCondDispatch measures the Cond middleware's per-request branch cost.
// Then and Else are passthroughs so the benchmark isolates the conditional.
func BenchmarkCondDispatch(b *testing.B) {
	cond := Cond{
		If:   func(*http.Request) bool { return true },
		Then: passthrough(),
		Else: passthrough(),
	}
	h := cond.ServeHandler(noopHandler)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := newDiscardResponseWriter()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

// discardResponseWriter is a no-op http.ResponseWriter for benchmarks that
// don't care about the response. Using httptest.NewRecorder() in a tight
// loop allocates a fresh recorder each iteration and dominates the result.
// The shared header map is reused across iterations — benchmarks that mutate
// headers should call ResetHeader between iterations or use a fresh writer.
type discardResponseWriter struct {
	h http.Header
}

func newDiscardResponseWriter() *discardResponseWriter {
	return &discardResponseWriter{h: make(http.Header)}
}

func (w *discardResponseWriter) Header() http.Header        { return w.h }
func (w *discardResponseWriter) Write(p []byte) (int, error) { return io.Discard.Write(p) }
func (w *discardResponseWriter) WriteHeader(int)             {}
