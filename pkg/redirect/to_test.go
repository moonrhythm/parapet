package redirect_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/redirect"
)

func TestRedirector(t *testing.T) {
	t.Parallel()

	t.Run("Redirect", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		To("https://example.com", http.StatusFound).
			ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
			ServeHTTP(w, r)
		assert.Equal(t, http.StatusFound, w.Code)
		assert.Equal(t, "https://example.com", w.Header().Get("Location"))
	})

	t.Run("Redirect default status code", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		To("https://example.com", 0).
			ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
			ServeHTTP(w, r)
		assert.Equal(t, http.StatusMovedPermanently, w.Code)
		assert.Equal(t, "https://example.com", w.Header().Get("Location"))
	})
}
