package location_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet"
	. "github.com/moonrhythm/parapet/pkg/location"
)

func TestExactMatcher(t *testing.T) {
	t.Parallel()

	t.Run("Matched", func(t *testing.T) {
		m := Exact("/path1")
		r := httptest.NewRequest("GET", "/path1", nil)
		w := httptest.NewRecorder()
		called := false
		m.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			})
		}))
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "should not be called")
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Unmatched", func(t *testing.T) {
		m := Exact("/path1")
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		called := false
		m.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Fail(t, "should not be called")
			})
		}))
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Unmatched with suffix", func(t *testing.T) {
		m := Exact("/path1")
		r := httptest.NewRequest("GET", "/path1/", nil)
		w := httptest.NewRecorder()
		called := false
		m.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Fail(t, "should not be called")
			})
		}))
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})
}
