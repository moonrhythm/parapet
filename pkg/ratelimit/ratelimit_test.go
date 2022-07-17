package ratelimit_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/ratelimit"
)

func TestRateLimiter(t *testing.T) {
	t.Parallel()

	t.Run("Pass", func(t *testing.T) {
		var (
			called = false
			take   = false
			put    = false
		)

		strategy := &mockStrategy{
			takeFunc: func(key string) bool {
				assert.Equal(t, "t", key)
				take = true
				return true
			},
			putFunc: func(key string) {
				assert.Equal(t, "t", key)
				put = true
			},
			afterFunc: func(key string) time.Duration {
				assert.Fail(t, "must not be called")
				return 0
			},
		}

		m := RateLimiter{
			Key: func(r *http.Request) string {
				return "t"
			},
			Strategy: strategy,
		}

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.True(t, take)
		assert.True(t, put)
	})

	t.Run("Exceed", func(t *testing.T) {
		strategy := &mockStrategy{
			takeFunc: func(key string) bool {
				return false
			},
			putFunc: func(key string) {
				assert.Fail(t, "must not be called")
			},
			afterFunc: func(key string) time.Duration {
				return 2 * time.Second
			},
		}

		exceed := false
		m := RateLimiter{
			Strategy: strategy,
			ExceededHandler: func(w http.ResponseWriter, r *http.Request, after time.Duration) {
				exceed = true
				assert.Equal(t, 2*time.Second, after)
				assert.NotNil(t, w)
				assert.NotNil(t, r)
			},
		}

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.True(t, exceed)
	})

	t.Run("Default Exceed", func(t *testing.T) {
		strategy := &mockStrategy{
			takeFunc: func(key string) bool {
				return false
			},
			putFunc: func(key string) {
				assert.Fail(t, "must not be called")
			},
			afterFunc: func(key string) time.Duration {
				return 2 * time.Second
			},
		}

		m := RateLimiter{
			Strategy: strategy,
		}

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.Equal(t, "2", w.Header().Get("Retry-After"))
		assert.Equal(t, http.StatusTooManyRequests, w.Code)
	})

	t.Run("Default Exceed no After", func(t *testing.T) {
		strategy := &mockStrategy{
			takeFunc: func(key string) bool {
				return false
			},
			putFunc: func(key string) {
				assert.Fail(t, "must not be called")
			},
			afterFunc: func(key string) time.Duration {
				return 0
			},
		}

		m := RateLimiter{
			Strategy: strategy,
		}

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.Empty(t, w.Header()["Retry-After"])
		assert.Equal(t, http.StatusTooManyRequests, w.Code)
	})

	t.Run("New should not return nil key", func(t *testing.T) {
		m := New(nil)
		assert.NotNil(t, m.Key)
	})

	t.Run("Release on write header", func(t *testing.T) {
		put := false
		strategy := &mockStrategy{
			takeFunc: func(key string) bool {
				return true
			},
			putFunc: func(key string) {
				put = true
			},
			afterFunc: func(key string) time.Duration {
				return 2 * time.Second
			},
		}

		m := RateLimiter{
			Strategy:             strategy,
			ReleaseOnWriteHeader: true,
		}

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.False(t, put)
			w.WriteHeader(http.StatusOK)
			assert.True(t, put)
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
	})

	t.Run("Release on write header without call write header", func(t *testing.T) {
		put := false
		strategy := &mockStrategy{
			takeFunc: func(key string) bool {
				return true
			},
			putFunc: func(key string) {
				put = true
			},
			afterFunc: func(key string) time.Duration {
				return 2 * time.Second
			},
		}

		m := RateLimiter{
			Strategy:             strategy,
			ReleaseOnWriteHeader: true,
		}

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		called := false
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.False(t, put)
			called = true
		})).ServeHTTP(w, r)
		assert.True(t, called)
		assert.True(t, put)
	})

	t.Run("Release on write header exceed", func(t *testing.T) {
		strategy := &mockStrategy{
			takeFunc: func(key string) bool {
				return false
			},
			putFunc: func(key string) {
				assert.Fail(t, "must not be called")
			},
			afterFunc: func(key string) time.Duration {
				return 2 * time.Second
			},
		}

		m := RateLimiter{
			Strategy:             strategy,
			ReleaseOnWriteHeader: true,
		}

		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		})).ServeHTTP(w, r)
		assert.Equal(t, http.StatusTooManyRequests, w.Code)
	})
}

type mockStrategy struct {
	takeFunc  func(key string) bool
	putFunc   func(key string)
	afterFunc func(key string) time.Duration
}

func (s *mockStrategy) Take(key string) bool {
	return s.takeFunc(key)
}

func (s *mockStrategy) Put(key string) {
	s.putFunc(key)
}

func (s *mockStrategy) After(key string) time.Duration {
	return s.afterFunc(key)
}

func TestClientIP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		Input  string
		Output string
	}{
		{"127.0.0.1", string(net.ParseIP("127.0.0.1"))},
		{"::1", string(net.ParseIP("::1"))},
		{"hello", "hello"},
	}

	for _, c := range cases {
		t.Run(c.Input, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("X-Real-Ip", c.Input)
			ip := ClientIP(r)
			assert.Equal(t, c.Output, ip)
		})
	}
}
