package headers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/headers"
)

func TestMapRequest(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Add("X-Header", "1")
	r.Header.Add("X-Header", "2")
	w := httptest.NewRecorder()
	called := false
	MapRequest("X-Header", func(v string) string {
		return "9" + v
	}).ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if assert.Len(t, r.Header["X-Header"], 2) {
			assert.Equal(t, "91", r.Header["X-Header"][0])
			assert.Equal(t, "92", r.Header["X-Header"][1])
		}
	})).ServeHTTP(w, r)
	assert.True(t, called)
}

func TestMapResponse(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		MapResponse("X-Header", func(v string) string {
			return "9" + v
		}).ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.Len(t, w.Header()["X-Header"], 0)
	})

	t.Run("Map", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		MapResponse("X-Header", func(v string) string {
			return "9" + v
		}).ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Header().Add("X-Header", "1")
			w.Header().Add("X-Header", "2")
		})).ServeHTTP(w, r)
		assert.True(t, called)
		if assert.Len(t, w.Header()["X-Header"], 2) {
			assert.Equal(t, "91", w.Header()["X-Header"][0])
			assert.Equal(t, "92", w.Header()["X-Header"][1])
		}
	})
}
