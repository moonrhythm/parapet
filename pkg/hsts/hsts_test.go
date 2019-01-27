package hsts_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/hsts"
)

func TestHSTS(t *testing.T) {
	t.Parallel()

	t.Run("Default", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		Default().ServeHandler(http.NotFoundHandler()).ServeHTTP(w, r)
		assert.NotEmpty(t, w.Header().Get("Strict-Transport-Security"))
		assert.Equal(t, "max-age=31536000", w.Header().Get("Strict-Transport-Security"))
	})

	t.Run("Preload", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		Preload().ServeHandler(http.NotFoundHandler()).ServeHTTP(w, r)
		assert.NotEmpty(t, w.Header().Get("Strict-Transport-Security"))
		assert.Equal(t, "max-age=63072000; includeSubDomains; preload", w.Header().Get("Strict-Transport-Security"))
	})

	t.Run("IncludeSubDomains", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		HSTS{
			MaxAge:            10 * time.Second,
			IncludeSubDomains: true,
		}.ServeHandler(http.NotFoundHandler()).ServeHTTP(w, r)
		assert.NotEmpty(t, w.Header().Get("Strict-Transport-Security"))
		assert.Equal(t, "max-age=10; includeSubDomains", w.Header().Get("Strict-Transport-Security"))
	})
}
