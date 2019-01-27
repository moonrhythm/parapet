package headers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/headers"
)

func TestDeleteRequest(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		DeleteRequest().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Del", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Header", "1")
		w := httptest.NewRecorder()
		called := false
		DeleteRequest("X-Header").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			assert.Empty(t, r.Header["X-Header"])
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})
}

func TestDeleteResponse(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		DeleteResponse().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Write After Delete", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		DeleteResponse("X-Header").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Header().Set("X-Header", "1")
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.Empty(t, w.Header()["X-Header"])
	})

	t.Run("Write Before Delete", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		w.Header().Set("X-Header", "1")
		called := false
		DeleteResponse("X-Header").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.Empty(t, w.Header()["X-Header"])
	})

	t.Run("Double call write header", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		DeleteResponse("X-Header").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Header().Set("X-Header", "0")
			w.WriteHeader(http.StatusOK)
			w.WriteHeader(http.StatusNotFound)
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.Empty(t, w.Header()["X-Header"])
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Write body", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		DeleteResponse("X-Header").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Header().Set("X-Header", "0")
			w.Write([]byte("test"))
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.Empty(t, w.Header()["X-Header"])
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "test", w.Body.String())
	})
}
