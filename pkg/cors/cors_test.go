package cors_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/cors"
)

func TestNewDefaults(t *testing.T) {
	t.Parallel()

	m := New()
	assert.True(t, m.AllowAllOrigins)
	assert.Equal(t, time.Hour, m.MaxAge)
	assert.Contains(t, m.AllowMethods, "GET")
	assert.Contains(t, m.AllowHeaders, "Authorization")
}

func TestAllowOrigins(t *testing.T) {
	t.Parallel()

	f := AllowOrigins("https://a.example.com", "https://b.example.com")
	assert.True(t, f("https://a.example.com"))
	assert.True(t, f("https://b.example.com"))
	assert.False(t, f("https://c.example.com"))
	assert.False(t, f(""))
}

func TestCORSAllowAllOrigins(t *testing.T) {
	t.Parallel()

	m := New()
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCORSNoOrigin(t *testing.T) {
	t.Parallel()

	called := false
	m := New()
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSPreflight(t *testing.T) {
	t.Parallel()

	called := false
	m := &CORS{
		AllowOrigins:     AllowOrigins("https://example.com"),
		AllowMethods:     []string{"GET", "POST"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		ExposeHeaders:    []string{"X-Custom"},
		AllowCredentials: true,
		MaxAge:           2 * time.Hour,
	}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("OPTIONS", "/", nil)
	r.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.False(t, called, "preflight should not invoke downstream")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://example.com", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET,POST", w.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Content-Type,Authorization", w.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "7200", w.Header().Get("Access-Control-Max-Age"))
}

func TestCORSPanicsOnAllowAllWithCredentials(t *testing.T) {
	t.Parallel()

	m := &CORS{
		AllowAllOrigins:  true,
		AllowCredentials: true,
	}
	assert.Panics(t, func() {
		m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	})
}

func TestCORSExposeHeadersOnNonPreflight(t *testing.T) {
	t.Parallel()

	m := &CORS{
		AllowAllOrigins: true,
		ExposeHeaders:   []string{"X-Custom", "X-Other"},
	}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, "X-Custom,X-Other", w.Header().Get("Access-Control-Expose-Headers"))
}

func TestCORSRestrictedOriginsAllowed(t *testing.T) {
	t.Parallel()

	called := false
	m := &CORS{
		AllowOrigins: AllowOrigins("https://allowed.example.com"),
		AllowMethods: []string{"GET"},
	}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://allowed.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "https://allowed.example.com", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", w.Header().Get("Vary"))
}

func TestCORSRestrictedOriginsForbidden(t *testing.T) {
	t.Parallel()

	called := false
	m := &CORS{
		AllowOrigins: AllowOrigins("https://allowed.example.com"),
	}
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://other.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.False(t, called)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCORSMissingOriginConfig(t *testing.T) {
	t.Parallel()

	m := &CORS{}
	assert.Panics(t, func() {
		m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	})
}

func TestCORSPreflightVaryWhenRestricted(t *testing.T) {
	t.Parallel()

	m := &CORS{
		AllowOrigins: AllowOrigins("https://allowed.example.com"),
		AllowMethods: []string{"GET"},
	}
	h := m.ServeHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	r := httptest.NewRequest("OPTIONS", "/", nil)
	r.Header.Set("Origin", "https://allowed.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	vary := w.Header().Values("Vary")
	assert.Contains(t, vary, "Origin")
	assert.Contains(t, vary, "Access-Control-Request-Method")
	assert.Contains(t, vary, "Access-Control-Request-Headers")
}
