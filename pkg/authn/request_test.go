package authn_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/moonrhythm/parapet/pkg/authn"
)

func TestRequest(t *testing.T) {
	t.Parallel()

	// start auth server
	{
		srv := http.Server{
			Addr: "127.0.0.1:8300",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				status := r.Header.Get("X-Return-Status")
				content := r.Header.Get("X-Return-Content")
				s, _ := strconv.Atoi(status)
				if s == 0 {
					s = 500
				}
				w.WriteHeader(s)
				w.Write([]byte(content))
			}),
		}
		go srv.ListenAndServe()
		time.Sleep(100 * time.Millisecond)
	}
	authURL, err := url.Parse("http://127.0.0.1:8300")
	require.NoError(t, err)

	t.Run("Unauthenticated", func(t *testing.T) {
		m := Request(authURL)

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Return-Status", "401")
		r.Header.Set("X-Return-Content", "unauthorized")
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.Equal(t, 401, w.Code)
		assert.Equal(t, "unauthorized", w.Body.String())
	})

	t.Run("Forbidden", func(t *testing.T) {
		m := Request(authURL)

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Return-Status", "403")
		r.Header.Set("X-Return-Content", "forbidden")
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.Equal(t, 403, w.Code)
		assert.Equal(t, "forbidden", w.Body.String())
	})

	t.Run("Authorized_200", func(t *testing.T) {
		m := Request(authURL)

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Return-Status", "200")
		r.Header.Set("X-Return-Content", "success")
		w := httptest.NewRecorder()
		called := false
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Write([]byte("ok"))
		})).ServeHTTP(w, r)
		assert.Equal(t, 200, w.Code)
		assert.True(t, called)
		assert.Equal(t, "ok", w.Body.String())
	})

	t.Run("Authorized_204", func(t *testing.T) {
		m := Request(authURL)

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Return-Status", "204")
		w := httptest.NewRecorder()
		called := false
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.Write([]byte("ok"))
		})).ServeHTTP(w, r)
		assert.Equal(t, 200, w.Code)
		assert.True(t, called)
		assert.Equal(t, "ok", w.Body.String())
	})
}
