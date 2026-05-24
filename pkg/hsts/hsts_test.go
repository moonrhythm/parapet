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

	t.Run("ShareValueSlice shares the value slice across requests", func(t *testing.T) {
		handler := HSTS{MaxAge: 10 * time.Second, ShareValueSlice: true}.
			ServeHandler(http.NotFoundHandler())

		w1 := httptest.NewRecorder()
		handler.ServeHTTP(w1, httptest.NewRequest("GET", "/", nil))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, httptest.NewRequest("GET", "/", nil))

		v1 := w1.Header()["Strict-Transport-Security"]
		v2 := w2.Header()["Strict-Transport-Security"]
		if assert.Len(t, v1, 1) && assert.Len(t, v2, 1) {
			assert.Equal(t, "max-age=10", v1[0])
			// Same backing array on every request — value slice built once.
			assert.Same(t, &v1[0], &v2[0])
		}
	})

	t.Run("default allocates a fresh value slice per request", func(t *testing.T) {
		handler := Default().ServeHandler(http.NotFoundHandler())

		w1 := httptest.NewRecorder()
		handler.ServeHTTP(w1, httptest.NewRequest("GET", "/", nil))
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, httptest.NewRequest("GET", "/", nil))

		v1 := w1.Header()["Strict-Transport-Security"]
		v2 := w2.Header()["Strict-Transport-Security"]
		if assert.Len(t, v1, 1) && assert.Len(t, v2, 1) {
			assert.Equal(t, v1[0], v2[0])
			// Distinct backing arrays — not shared when the flag is off.
			assert.NotSame(t, &v1[0], &v2[0])
		}
	})
}
