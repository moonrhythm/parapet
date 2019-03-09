package headers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/headers"
)

func TestInterceptRequest(t *testing.T) {
	t.Parallel()

	t.Run("Nil Interceptor", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		InterceptRequest(nil).ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Intercept", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		intercepted := false
		InterceptRequest(func(h http.Header) {
			assert.False(t, called)
			intercepted = true
			h.Set("X", "1")
		}).ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "1", r.Header.Get("X"))
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, intercepted)
		assert.True(t, called)
	})
}

func TestInterceptResponse(t *testing.T) {
	t.Parallel()

	t.Run("Nil Interceptor", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		InterceptResponse(nil).ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Intercept", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		intercepted := false
		InterceptResponse(func(w http.ResponseWriter, statusCode int) {
			h := w.Header()
			assert.True(t, called)
			assert.Equal(t, 200, statusCode)
			intercepted = true
			h.Set("X", "1")
		}).ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Empty(t, w.Header().Get("X"))
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, intercepted)
		assert.True(t, called)
		assert.Equal(t, "1", w.Header().Get("X"))
	})

	t.Run("Intercept on WriteHeader", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		intercepted := false
		InterceptResponse(func(w http.ResponseWriter, statusCode int) {
			h := w.Header()
			assert.True(t, called)
			assert.Equal(t, 400, statusCode)
			intercepted = true
			h.Set("X", "1")
		}).ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Empty(t, w.Header().Get("X"))
			called = true
			w.WriteHeader(400)
		})).ServeHTTP(w, r)
		assert.True(t, intercepted)
		assert.True(t, called)
		assert.Equal(t, "1", w.Header().Get("X"))
	})
}
