package parapet_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet"
)

func tagMiddleware(tag string) Middleware {
	return MiddlewareFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("<" + tag + ">"))
			next.ServeHTTP(w, r)
			_, _ = w.Write([]byte("</" + tag + ">"))
		})
	})
}

func TestMiddlewaresAppliedInOrder(t *testing.T) {
	t.Parallel()

	var ms Middlewares
	ms.Use(tagMiddleware("a"))
	ms.Use(tagMiddleware("b"))
	ms.Use(tagMiddleware("c"))

	h := ms.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("X"))
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, "<a><b><c>X</c></b></a>", w.Body.String())
}

func TestMiddlewaresIgnoresNil(t *testing.T) {
	t.Parallel()

	var ms Middlewares
	ms.Use(nil)
	ms.Use(tagMiddleware("a"))
	ms.Use(nil)
	assert.Len(t, ms, 1)

	h := ms.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("X"))
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, "<a>X</a>", w.Body.String())
}

func TestMiddlewaresUseFunc(t *testing.T) {
	t.Parallel()

	var ms Middlewares
	ms.UseFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("F"))
			next.ServeHTTP(w, r)
		})
	})

	h := ms.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("X"))
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, "FX", w.Body.String())
}

func TestCondThen(t *testing.T) {
	t.Parallel()

	c := Cond{
		If:   func(r *http.Request) bool { return r.URL.Path == "/then" },
		Then: tagMiddleware("then"),
		Else: tagMiddleware("else"),
	}
	h := c.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("X"))
	}))

	t.Run("then", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/then", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, "<then>X</then>", w.Body.String())
	})
	t.Run("else", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/other", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, "<else>X</else>", w.Body.String())
	})
}

func TestCondElseDefaultsToPassthrough(t *testing.T) {
	t.Parallel()

	c := Cond{
		If:   func(r *http.Request) bool { return false },
		Then: tagMiddleware("then"),
	}
	h := c.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain"))
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, "plain", w.Body.String())
}

func TestHandler(t *testing.T) {
	t.Parallel()

	h := Handler(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hi"))
	})

	// ServeHandler ignores the inner handler since Handler is terminal
	served := h.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler must not be called")
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	served.ServeHTTP(w, r)

	assert.Equal(t, "hi", w.Body.String())

	// also works as plain http.Handler
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r)
	assert.Equal(t, "hi", w2.Body.String())
}

func TestMiddlewareFuncServeHandler(t *testing.T) {
	t.Parallel()

	called := false
	mf := MiddlewareFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	})

	h := mf.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called)
	assert.Equal(t, "ok", w.Body.String())
}
