package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"time"
)

// New creates new rate limiter
func New(strategy Strategy) *RateLimiter {
	return &RateLimiter{
		Key:      ClientIP,
		Strategy: strategy,
	}
}

// RateLimiter middleware
type RateLimiter struct {
	Key             func(r *http.Request) string
	ExceededHandler ExceededHandler
	Strategy        Strategy
}

// Strategy interface
type Strategy interface {
	// Take returns true if success
	Take(key string) bool

	// Put calls after finished
	Put(key string)

	// After returns next time that can take again
	After(key string) time.Duration
}

// ExceededHandler type
type ExceededHandler func(w http.ResponseWriter, r *http.Request, after time.Duration)

func defaultExceededHandler(w http.ResponseWriter, r *http.Request, after time.Duration) {
	if after > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(after/time.Second), 10))
	}
	http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
}

func defaultKey(*http.Request) string {
	return ""
}

// ClientIP returns client ip from request
func ClientIP(r *http.Request) string {
	ipStr := r.Header.Get("X-Forwarded-For")
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ipStr
	}
	return string(ip)
}

// ServeHandler implements middleware interface
func (m *RateLimiter) ServeHandler(h http.Handler) http.Handler {
	if m.Key == nil {
		m.Key = defaultKey
	}
	if m.ExceededHandler == nil {
		m.ExceededHandler = defaultExceededHandler
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := m.Key(r)
		if !m.Strategy.Take(key) {
			m.ExceededHandler(w, r, m.Strategy.After(key))
			return
		}
		defer m.Strategy.Put(key) // use defer to always put token back when panic

		h.ServeHTTP(w, r)
	})
}
