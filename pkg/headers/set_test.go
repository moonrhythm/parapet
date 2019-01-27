package headers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/headers"
)

func TestSetRequest(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		SetRequest().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Set", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		SetRequest("X-Header", "1").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			assert.Equal(t, "1", r.Header.Get("X-Header"))
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})
}

func TestSetResponse(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		SetResponse().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Set", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		SetResponse("X-Header", "1").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Header().Set("X-Header", "0")
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.Equal(t, "1", w.Header().Get("X-Header"))
	})

	t.Run("Double call write header", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		SetResponse("X-Header", "1").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Header().Set("X-Header", "0")
			w.WriteHeader(http.StatusOK)
			w.WriteHeader(http.StatusNotFound)
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.Equal(t, "1", w.Header().Get("X-Header"))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Write body", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		SetResponse("X-Header", "1").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Header().Set("X-Header", "0")
			w.Write([]byte("test"))
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.Equal(t, "1", w.Header().Get("X-Header"))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "test", w.Body.String())
	})
}
