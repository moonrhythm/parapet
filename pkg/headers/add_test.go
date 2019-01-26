package headers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/headers"
)

func TestRequestAdder(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		AddRequest().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Set", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		AddRequest("X-Header", "1", "X-Header", "2").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if assert.Len(t, r.Header["X-Header"], 2) {
				assert.Equal(t, "1", r.Header["X-Header"][0])
				assert.Equal(t, "2", r.Header["X-Header"][1])
			}
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})
}

func TestResponseAdder(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		AddResponse().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Set", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		AddResponse("X-Header", "1", "X-Header", "2").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Header().Set("X-Header", "0")
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r)
		assert.True(t, called)
		if assert.Len(t, w.Header()["X-Header"], 3) {
			assert.Equal(t, "0", w.Header()["X-Header"][0])
			assert.Equal(t, "1", w.Header()["X-Header"][1])
			assert.Equal(t, "2", w.Header()["X-Header"][2])
		}
	})

	t.Run("Double call write header", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		AddResponse("X-Header", "1").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
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
		AddResponse("X-Header", "1").ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Write([]byte("test"))
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.Equal(t, "1", w.Header().Get("X-Header"))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "test", w.Body.String())
	})
}
