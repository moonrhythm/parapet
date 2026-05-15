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

func TestParseCIDRsSkipsInvalid(t *testing.T) {
	t.Parallel()

	ns := parseCIDRs([]string{"not-a-cidr", "1.1.1.1/32", ""})
	if assert.Len(t, ns, 1) {
		assert.Equal(t, "1.1.1.1/32", ns[0].String())
	}
}

func TestFirstHost(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1.2.3.4", firstHost("1.2.3.4, 5.6.7.8, 9.9.9.9"))
	assert.Equal(t, "1.2.3.4", firstHost("1.2.3.4"))
	assert.Equal(t, "", firstHost(""))
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
