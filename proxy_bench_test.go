package parapet

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The proxy middleware runs on every request the server handles, so its
// per-request cost — header reads/writes for X-Forwarded-For, X-Real-Ip,
// X-Forwarded-Proto — sets a floor on the framework's overhead.

var benchNoopHandler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

func benchProxy(b *testing.B, p *proxy, prep func(r *http.Request)) {
	b.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	if prep != nil {
		prep(r)
	}
	w := newBenchProxyRW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.ServeHTTP(w, r)
	}
}

// BenchmarkProxyDistrust covers the most common shape: the caller is not a
// trusted proxy, so X-Forwarded-* are unconditionally overwritten from
// r.RemoteAddr / r.TLS. Three header writes.
func BenchmarkProxyDistrust(b *testing.B) {
	p := &proxy{Handler: benchNoopHandler}
	benchProxy(b, p, nil)
}

// BenchmarkProxyTrustNoHeaders measures the trust path when the upstream
// did not supply X-Forwarded-* at all — every read returns empty, so the
// branch writes all three headers.
func BenchmarkProxyTrustNoHeaders(b *testing.B) {
	p := &proxy{Trust: Trusted(), Handler: benchNoopHandler}
	benchProxy(b, p, nil)
}

// BenchmarkProxyTrustWithHeaders measures the trust path when the upstream
// already supplied X-Forwarded-* — the reads short-circuit each branch and
// no writes happen. This is the typical edge → backend hop cost.
func BenchmarkProxyTrustWithHeaders(b *testing.B) {
	p := &proxy{Trust: Trusted(), Handler: benchNoopHandler}
	benchProxy(b, p, func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "203.0.113.5")
		r.Header.Set("X-Real-Ip", "203.0.113.5")
		r.Header.Set("X-Forwarded-Proto", "https")
	})
}

// BenchmarkProxyDistrustShared is BenchmarkProxyDistrust with the shared
// X-Forwarded-Proto slice enabled: the write reuses a global slice, dropping
// one of the three per-request allocations.
func BenchmarkProxyDistrustShared(b *testing.B) {
	p := &proxy{Handler: benchNoopHandler, shareProtoSlice: true}
	benchProxy(b, p, nil)
}

type benchProxyRW struct{ h http.Header }

func newBenchProxyRW() *benchProxyRW                  { return &benchProxyRW{h: make(http.Header)} }
func (w *benchProxyRW) Header() http.Header           { return w.h }
func (w *benchProxyRW) Write(p []byte) (int, error)   { return io.Discard.Write(p) }
func (w *benchProxyRW) WriteHeader(int)               {}
