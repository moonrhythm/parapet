package upstream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUpstream(t *testing.T) {
	t.Parallel()

	t.Run("Basic", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()

		called := false
		New(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
			called = true
			assert.Equal(t, "/path", r.URL.Path)
			return w.Result(), nil
		})).ServeHandler(nil).ServeHTTP(w, r)

		assert.True(t, called)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "ok", w.Body.String())
	})

	t.Run("Prefix Path", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path?p=1", nil)
		w := httptest.NewRecorder()

		called := false
		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				assert.Equal(t, "/prefix/path", r.URL.Path)
				assert.Equal(t, "1", r.URL.Query().Get("p"))
				return httptest.NewRecorder().Result(), nil
			}),
			Path: "/prefix",
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Prefix Path with both query", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path?p=1", nil)
		w := httptest.NewRecorder()

		called := false
		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				assert.Equal(t, "/prefix/path", r.URL.Path)
				assert.Equal(t, "1", r.URL.Query().Get("p"))
				assert.Equal(t, "2", r.URL.Query().Get("q"))
				return httptest.NewRecorder().Result(), nil
			}),
			Path: "/prefix?q=2",
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Override Host", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		called := false
		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				assert.Equal(t, "www.google.com", r.Host)
				return httptest.NewRecorder().Result(), nil
			}),
			Host: "www.google.com",
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Error", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("can not dial to server")
			}),
			Host: "www.google.com",
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadGateway, w.Code)
	})
}
