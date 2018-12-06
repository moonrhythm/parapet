package ratelimit

import (
	"net/http"
	"strconv"
	"time"
)

// RateLimiter middleware
type RateLimiter struct {
	Key             func(r *http.Request) string
	ExceededHandler ExceededHandler
	Bucket          Bucket
}

// Bucket interface
type Bucket interface {
	Take(key string) bool
	After(key string) time.Duration
}

// ExceededHandler type
type ExceededHandler func(w http.ResponseWriter, r *http.Request, after time.Duration)

// ServeHandler implements middleware interface
func (m *RateLimiter) ServeHandler(h http.Handler) http.Handler {
	if m.Key == nil {
		m.Key = func(*http.Request) string {
			return ""
		}
	}

	if m.ExceededHandler == nil {
		m.ExceededHandler = func(w http.ResponseWriter, r *http.Request, after time.Duration) {
			w.Header().Set("Retry-After", strconv.FormatInt(int64(after/time.Second), 10))
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := m.Key(r)
		if !m.Bucket.Take(key) {
			m.ExceededHandler(w, r, m.Bucket.After(key))
			return
		}

		h.ServeHTTP(w, r)
	})
}
