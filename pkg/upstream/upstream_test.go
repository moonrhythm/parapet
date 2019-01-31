package upstream

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/logger"
)

func TestUpstream(t *testing.T) {
	t.Parallel()

	t.Run("Basic", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()

		called := false
		New(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
			called = true
			assert.Equal(t, "/path", r.URL.Path)
			return w.Result(), nil
		})).ServeHandler(nil).ServeHTTP(w, r)

		assert.True(t, called)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "ok", w.Body.String())
	})

	t.Run("Prefix Path", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path?p=1", nil)
		w := httptest.NewRecorder()

		called := false
		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				assert.Equal(t, "/prefix/path", r.URL.Path)
				assert.Equal(t, "1", r.URL.Query().Get("p"))
				return httptest.NewRecorder().Result(), nil
			}),
			Path: "/prefix",
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Prefix Path with both query", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path?p=1", nil)
		w := httptest.NewRecorder()

		called := false
		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				assert.Equal(t, "/prefix/path", r.URL.Path)
				assert.Equal(t, "1", r.URL.Query().Get("p"))
				assert.Equal(t, "2", r.URL.Query().Get("q"))
				return httptest.NewRecorder().Result(), nil
			}),
			Path: "/prefix?q=2",
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Override Host", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		called := false
		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				called = true
				assert.Equal(t, "www.google.com", r.Host)
				return httptest.NewRecorder().Result(), nil
			}),
			Host: "www.google.com",
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Error", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("can not dial to server")
			}),
			Host: "www.google.com",
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.Equal(t, http.StatusBadGateway, w.Code)
	})

	t.Run("Retry", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		cnt := 0
		start := time.Now()
		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				cnt++
				return nil, fmt.Errorf("can not dial to server")
			}),
			Retries:       3,
			BackoffFactor: 50 * time.Millisecond,
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.Equal(t, 4, cnt)
		assert.WithinDuration(t, start.Add((50+100+200)*time.Millisecond), time.Now(), 20*time.Millisecond)
	})

	t.Run("Should not retry non-idempotent", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", nil)
		w := httptest.NewRecorder()

		cnt := 0
		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				cnt++
				return nil, fmt.Errorf("can not dial to server")
			}),
			Retries:       3,
			BackoffFactor: 50 * time.Millisecond,
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.Equal(t, 1, cnt)
	})

	t.Run("Should not retry idempotent non-empty body", func(t *testing.T) {
		r := httptest.NewRequest("PUT", "/", bytes.NewReader([]byte("test")))
		w := httptest.NewRecorder()

		cnt := 0
		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				cnt++
				return nil, fmt.Errorf("can not dial to server")
			}),
			Retries:       3,
			BackoffFactor: 50 * time.Millisecond,
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.Equal(t, 1, cnt)
	})

	t.Run("Unavailable", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		Upstream{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return nil, ErrUnavailable
			}),
		}.ServeHandler(nil).ServeHTTP(w, r)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})

	t.Run("Log Upstream", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/path", nil)
		w := httptest.NewRecorder()

		var upstreamServer string

		ms := parapet.Middlewares{}
		ms.Use(logger.Stdout())
		ms.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				h.ServeHTTP(w, r)
				upstreamServer, _ = logger.Get(r.Context(), "upstream").(string)
			})
		}))
		ms.Use(New(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			r.URL.Host = "server1"
			return httptest.NewRecorder().Result(), nil
		})))
		ms.ServeHandler(nil).ServeHTTP(w, r)

		assert.Equal(t, "server1", upstreamServer)
	})
}
