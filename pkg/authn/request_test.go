package authn_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

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
				s, _ := strconv.Atoi(status)
				if s == 0 {
					s = 500
				}
				w.WriteHeader(s)
			}),
		}
		go srv.ListenAndServe()
	}
	authURL, err := url.Parse("http://127.0.0.1:8300")
	require.NoError(t, err)

	t.Run("Unauthenticated", func(t *testing.T) {
		m := Request(authURL)

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Return-Status", "401")
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.Equal(t, 401, w.Code)
	})

	t.Run("Forbidden", func(t *testing.T) {
		m := Request(authURL)

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Return-Status", "403")
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.Equal(t, 403, w.Code)
	})

	t.Run("Authorized_200", func(t *testing.T) {
		m := Request(authURL)

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Return-Status", "200")
		w := httptest.NewRecorder()
		called := false
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.Equal(t, 200, w.Code)
		assert.True(t, called)
	})

	t.Run("Authorized_204", func(t *testing.T) {
		m := Request(authURL)

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Return-Status", "204")
		w := httptest.NewRecorder()
		called := false
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.Equal(t, 200, w.Code)
		assert.True(t, called)
	})
}
