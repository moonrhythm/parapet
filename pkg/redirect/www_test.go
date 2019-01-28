package redirect_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/redirect"
)

func TestWWWRedirector(t *testing.T) {
	t.Parallel()

	t.Run("Redirect HTTP Non-WWW to WWW", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		r.Host = "example.com"
		r.Header.Set(xfp, "http")
		WWW().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusMovedPermanently, w.Code)
		assert.Equal(t, "http://www.example.com/path", w.Header().Get("Location"))
	})

	t.Run("Redirect HTTPS Non-WWW to WWW", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		r.Host = "example.com"
		r.Header.Set(xfp, "https")
		WWW().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusMovedPermanently, w.Code)
		assert.Equal(t, "https://www.example.com/path", w.Header().Get("Location"))
	})

	t.Run("Not Redirect WWW", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		r.Host = "www.example.com"
		r.Header.Set(xfp, "http")
		WWW().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Header().Get("Location"))
	})
}
