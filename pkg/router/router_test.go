package router_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet"
	. "github.com/moonrhythm/parapet/pkg/router"
)

func handler(body string) parapet.Middleware {
	return parapet.MiddlewareFunc(func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		})
	})
}

func TestRouterRoutesByPrefix(t *testing.T) {
	t.Parallel()

	m := New()
	m.Handle("/a", handler("got-a"))
	m.Handle("/b/", handler("got-b"))

	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fallback"))
	})
	h := m.ServeHandler(fallback)

	cases := []struct {
		path string
		want string
	}{
		{"/a", "got-a"},
		{"/a/", "got-a"},
		{"/a/sub", "got-a"},
		{"/b/", "got-b"},
		{"/b/sub", "got-b"},
		{"/c", "fallback"},
		{"/", "fallback"},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			body, _ := io.ReadAll(w.Body)
			assert.Equal(t, tc.want, string(body))
		})
	}
}

func TestRouterOverridesRoot(t *testing.T) {
	t.Parallel()

	m := New()
	m.Handle("/", handler("root"))

	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fallback"))
	})
	h := m.ServeHandler(fallback)

	r := httptest.NewRequest("GET", "/anything", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, _ := io.ReadAll(w.Body)
	assert.Equal(t, "root", string(body))
}

func TestRouterEmpty(t *testing.T) {
	t.Parallel()

	m := New()
	called := false
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	r := httptest.NewRequest("GET", "/anything", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.True(t, called)
}
