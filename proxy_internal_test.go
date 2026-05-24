package parapet

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProxyDistrust(t *testing.T) {
	t.Parallel()

	t.Run("Default", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Real-Ip", "10.0.1.1")
		r.Header.Set("X-Forwarded-For", "10.0.1.2")
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		called := false
		(&proxy{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				assert.NotEqual(t, "10.0.1.1", r.Header.Get("X-Real-Ip"))
				assert.NotContains(t, r.Header.Get("X-Forwarded-For"), "10.0.1.2")
				assert.Equal(t, "http", r.Header.Get("X-Forwarded-Proto"))
			}),
		}).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Distrust", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Real-Ip", "10.0.1.1")
		r.Header.Set("X-Forwarded-For", "10.0.1.2")
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		called := false
		(&proxy{
			Trust: func(r *http.Request) bool {
				return false
			},
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				assert.NotEqual(t, "10.0.1.1", r.Header.Get("X-Real-Ip"))
				assert.NotContains(t, r.Header.Get("X-Forwarded-For"), "10.0.1.2")
				assert.Equal(t, "http", r.Header.Get("X-Forwarded-Proto"))
			}),
		}).ServeHTTP(w, r)
		assert.True(t, called)
	})
}

func TestProxyTrust(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Real-Ip", "10.0.1.1")
	r.Header.Set("X-Forwarded-For", "10.0.1.2")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	called := false
	(&proxy{
		Trust: Trusted(),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			assert.Equal(t, "10.0.1.1", r.Header.Get("X-Real-Ip"))
			assert.Contains(t, r.Header.Get("X-Forwarded-For"), "10.0.1.2")
			assert.Equal(t, "https", r.Header.Get("X-Forwarded-Proto"))
		}),
	}).ServeHTTP(w, r)
	assert.True(t, called)
}

func TestParseCIDRs(t *testing.T) {
	t.Parallel()

	ns := parseCIDRs([]string{"1.1.1.1/32", "8.8.0.0/16"})
	if assert.Len(t, ns, 2) {
		assert.Equal(t, "1.1.1.1/32", ns[0].String())
		assert.Equal(t, "8.8.0.0/16", ns[1].String())
	}
}

func TestParseCIDRsPanicsOnInvalid(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		parseCIDRs([]string{"not-a-cidr"})
	})
	assert.Panics(t, func() {
		parseCIDRs([]string{"1.1.1.1/32", ""})
	})
}

func TestFirstHost(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1.2.3.4", firstHost("1.2.3.4, 5.6.7.8, 9.9.9.9"))
	assert.Equal(t, "1.2.3.4", firstHost("1.2.3.4"))
	assert.Equal(t, "", firstHost(""))
	// Leading whitespace from " hop1, hop2" or after split must be stripped.
	assert.Equal(t, "1.2.3.4", firstHost(" 1.2.3.4 , 5.6.7.8"))
	assert.Equal(t, "1.2.3.4", firstHost("\t1.2.3.4"))
}

func TestParseHost(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1.2.3.4", parseHost("1.2.3.4:5678"))
	assert.Equal(t, "", parseHost("garbage"))
}

func TestProxyTrustComputesXFFWhenMissing(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	called := false
	(&proxy{
		Trust:                   Trusted(),
		ComputeFullForwardedFor: true,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			assert.Equal(t, "10.0.0.1", r.Header.Get("X-Forwarded-For"))
			assert.Equal(t, "10.0.0.1", r.Header.Get("X-Real-Ip"))
			assert.Equal(t, "http", r.Header.Get("X-Forwarded-Proto"))
		}),
	}).ServeHTTP(w, r)
	assert.True(t, called)
}

func TestProxyTrustComputesXFFAppendsRemote(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 1.2.3.4")
	w := httptest.NewRecorder()
	(&proxy{
		Trust:                   Trusted(),
		ComputeFullForwardedFor: true,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "203.0.113.5, 1.2.3.4, 10.0.0.1", r.Header.Get("X-Forwarded-For"))
			// X-Real-Ip is filled from the first hop in XFF since it was empty
			assert.Equal(t, "203.0.113.5", r.Header.Get("X-Real-Ip"))
		}),
	}).ServeHTTP(w, r)
}

// The proxy must not share value slices between X-Forwarded-For and
// X-Real-Ip in distrust: downstream code that mutates one header's slice
// in place (or appends with cap room) must not surprise the other.
func TestProxyDistrustWritesIndependentSlices(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	(&proxy{
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			xff := r.Header["X-Forwarded-For"]
			xri := r.Header["X-Real-Ip"]
			if assert.Len(t, xff, 1) && assert.Len(t, xri, 1) {
				// Mutating XFF's backing array must not leak into XRI.
				xff[0] = "mutated"
				assert.Equal(t, "10.0.0.1", xri[0])
			}
		}),
	}).ServeHTTP(httptest.NewRecorder(), r)
}

// The proxy must not write package-global slices into r.Header — any
// downstream in-place mutation would corrupt every future request.
func TestProxyXForwardedProtoIsNotGlobalSlice(t *testing.T) {
	t.Parallel()

	r1 := httptest.NewRequest("GET", "/", nil)
	r1.RemoteAddr = "10.0.0.1:12345"
	(&proxy{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		v := r.Header["X-Forwarded-Proto"]
		if assert.Len(t, v, 1) {
			v[0] = "mutated"
		}
	})}).ServeHTTP(httptest.NewRecorder(), r1)

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "10.0.0.2:12345"
	(&proxy{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "http", r.Header.Get("X-Forwarded-Proto"))
	})}).ServeHTTP(httptest.NewRecorder(), r2)
}

// sameBacking reports whether a and b share the same backing array (same first
// element address), i.e. one is the other and not an independent copy.
func sameBacking(a, b []string) bool {
	return len(a) > 0 && len(b) > 0 && &a[0] == &b[0]
}

// TestProtoValue pins the contract of (*proxy).protoValue in both states: a
// fresh per-request slice by default, and the shared package-global slice when
// shareProtoSlice is set on the proxy.
func TestProtoValue(t *testing.T) {
	t.Parallel()

	off := &proxy{}
	if got := off.protoValue(false); assert.Equal(t, []string{"http"}, got) {
		assert.False(t, sameBacking(got, xfpHTTP), "default must return a fresh http slice")
	}
	if got := off.protoValue(true); assert.Equal(t, []string{"https"}, got) {
		assert.False(t, sameBacking(got, xfpHTTPS), "default must return a fresh https slice")
	}

	on := &proxy{shareProtoSlice: true}
	assert.True(t, sameBacking(on.protoValue(false), xfpHTTP), "shared must return the global http slice")
	assert.True(t, sameBacking(on.protoValue(true), xfpHTTPS), "shared must return the global https slice")
}

// TestProxySharedProtoSlice verifies the end-to-end behaviour with sharing on:
// X-Forwarded-Proto is the shared global slice, while X-Forwarded-For and
// X-Real-Ip remain freshly allocated and independent (they carry the dynamic
// remote IP and must never be shared).
func TestProxySharedProtoSlice(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	(&proxy{
		shareProtoSlice: true,
		Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			assert.True(t, sameBacking(r.Header["X-Forwarded-Proto"], xfpHTTP),
				"X-Forwarded-Proto should be the shared global slice")

			xff := r.Header["X-Forwarded-For"]
			xri := r.Header["X-Real-Ip"]
			if assert.Len(t, xff, 1) && assert.Len(t, xri, 1) {
				assert.Equal(t, "10.0.0.1", xff[0])
				// XFF/XRI stay independent and fresh even with sharing on.
				assert.False(t, sameBacking(xff, xri))
				xff[0] = "mutated"
				assert.Equal(t, "10.0.0.1", xri[0])
			}
		}),
	}).ServeHTTP(httptest.NewRecorder(), r)
}

// TestServerEnableSharedProtoSlice covers the Server-level wiring: the setting
// is per-server, off by default, threaded into the proxy that configHandler
// builds, and locked once the server starts serving.
func TestServerEnableSharedProtoSlice(t *testing.T) {
	t.Parallel()

	t.Run("off by default", func(t *testing.T) {
		s := &Server{}
		s.configHandler()
		if p, ok := s.s.Handler.(*proxy); assert.True(t, ok) {
			assert.False(t, p.shareProtoSlice)
		}
	})

	t.Run("enabled threads into the proxy", func(t *testing.T) {
		s := &Server{}
		s.EnableSharedProtoSlice()
		assert.True(t, s.sharedProtoSlice)
		s.configHandler()
		if p, ok := s.s.Handler.(*proxy); assert.True(t, ok) {
			assert.True(t, p.shareProtoSlice)
		}
	})

	t.Run("panics after serve", func(t *testing.T) {
		s := &Server{}
		s.configHandler() // builds the handler, as the first request would
		assert.Panics(t, func() { s.EnableSharedProtoSlice() })
	})
}
