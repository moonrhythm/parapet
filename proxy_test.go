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
