package block_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet"
	. "github.com/moonrhythm/parapet/pkg/block"
)

func TestBlock(t *testing.T) {
	t.Parallel()

	t.Run("Match", func(t *testing.T) {
		m := New(func(r *http.Request) bool {
			assert.NotNil(t, r)
			return true
		})

		called := false
		m.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			})
		}))

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Not Match", func(t *testing.T) {
		m := New(func(r *http.Request) bool {
			assert.NotNil(t, r)
			return false
		})

		m.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Fail(t, "must not be called")
			})
		}))

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Catch-all", func(t *testing.T) {
		m := New(nil)

		called := false
		m.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
			})
		}))

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})
}
