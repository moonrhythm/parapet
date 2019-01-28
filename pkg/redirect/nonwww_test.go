package redirect_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/redirect"
)

func TestNonWWWRedirector(t *testing.T) {
	t.Parallel()

	t.Run("Redirect HTTP WWW to Non-WWW", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		r.Host = "www.example.com"
		r.Header.Set(xfp, "http")
		NonWWW().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusMovedPermanently, w.Code)
		assert.Equal(t, "http://example.com/path", w.Header().Get("Location"))
	})

	t.Run("Redirect HTTPS WWW to Non-WWW", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		r.Host = "www.example.com"
		r.Header.Set(xfp, "https")
		NonWWW().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusMovedPermanently, w.Code)
		assert.Equal(t, "https://example.com/path", w.Header().Get("Location"))
	})

	t.Run("Not Redirect Non-WWW", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		r.Host = "example.com"
		r.Header.Set(xfp, "http")
		NonWWW().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Header().Get("Location"))
	})
}
