package headers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/headers"
)

// SetRequest must allocate a fresh value slice per request: downstream
// middleware like MapRequest mutates header value slices in place, so any
// shared/pre-built slice would leak mutations across requests.
func TestSetRequestNoSharedSliceAcrossRequests(t *testing.T) {
	t.Parallel()

	set := SetRequest("X-Custom", "value")
	mapUpper := MapRequest("X-Custom", strings.ToUpper)
	chain := set.ServeHandler(mapUpper.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))

	// First request: MapRequest mutates the value slice in place.
	chain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	// Second request through the same SetRequest must still see "value".
	got := ""
	set.ServeHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Custom")
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	assert.Equal(t, "value", got)
}

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
