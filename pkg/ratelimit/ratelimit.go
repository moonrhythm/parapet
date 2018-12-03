package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"time"
)

// RateLimit middleware
type RateLimit struct {
	Key           func(r *http.Request) string
	Rate          int
	Unit          time.Duration
	ExceedHandler http.Handler

	bucket bucket
}

// New creates new rate limiter with default config
func New(rate int, unit time.Duration, trustProxy bool) *RateLimit {
	m := &RateLimit{
		Key: func(r *http.Request) string {
			return parseHost(r.RemoteAddr)
		},
		Rate: rate,
		Unit: unit,
	}
	if trustProxy {
		m.Key = func(r *http.Request) string {
			ip := r.Header.Get("X-Forwarded-For")
			if ip == "" {
				ip = parseHost(r.RemoteAddr)
			}
			return ip
		}
	}
	return m
}

// PerSecond creates new rate limiter per second
func PerSecond(rate int, trustProxy bool) *RateLimit {
	return New(rate, time.Second, trustProxy)
}

// PerMinute creates new rate limiter per minute
func PerMinute(rate int, trustProxy bool) *RateLimit {
	return New(rate, time.Minute, trustProxy)
}

// PerHour creates new rate limiter per hour
func PerHour(rate int, trustProxy bool) *RateLimit {
	return New(rate, time.Hour, trustProxy)
}

// ServeHandler implements middleware interface
func (m *RateLimit) ServeHandler(h http.Handler) http.Handler {
	if m.Key == nil || m.Rate <= 0 || m.Unit <= 0 {
		return h
	}

	if m.ExceedHandler == nil {
		m.ExceedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			now := time.Now()
			after := (now.Truncate(m.Unit).UnixNano() + int64(m.Unit) - now.UnixNano()) / int64(time.Second)
			if after <= 0 {
				after = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(after, 10))
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := time.Now().UnixNano() / int64(m.Unit)
		key := m.Key(r)

		current := m.bucket.Incr(t, key, m.Rate)
		if current > m.Rate {
			m.ExceedHandler.ServeHTTP(w, r)
			return
		}

		h.ServeHTTP(w, r)
	})
}

func parseHost(s string) string {
	host, _, _ := net.SplitHostPort(s)
	return host
}
