package redirect_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/redirect"
)

const xfp = "X-Forwarded-Proto"

func TestHTTPSRedirector(t *testing.T) {
	t.Parallel()

	t.Run("Redirect HTTP to HTTPS", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		r.Host = "example.com"
		r.Header.Set(xfp, "http")
		HTTPS().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusMovedPermanently, w.Code)
		assert.Equal(t, "https://example.com/path", w.Header().Get("Location"))
	})

	t.Run("Not Redirect HTTPS", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()
		r.Host = "example.com"
		r.Header.Set(xfp, "https")
		HTTPS().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Header().Get("Location"))
	})
}
