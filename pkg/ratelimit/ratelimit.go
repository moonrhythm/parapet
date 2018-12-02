package ratelimit

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang/groupcache/lru"
)

// RateLimit middleware
type RateLimit struct {
	Key           func(r *http.Request) string
	Rate          int
	Unit          time.Duration
	ExceedHandler http.Handler
	MaxEntries    int

	mu     sync.RWMutex
	bucket *lru.Cache
}

// New creates new default rate limiter
func New(ratePerSecond int, trustProxy bool) *RateLimit {
	m := &RateLimit{
		Key: func(r *http.Request) string {
			return r.RemoteAddr
		},
		Rate:       ratePerSecond,
		Unit:       time.Second,
		MaxEntries: 100000,
	}
	if trustProxy {
		m.Key = func(r *http.Request) string {
			ip := r.Header.Get("X-Forwarded-For")
			if ip == "" {
				ip = r.RemoteAddr
			}
			return ip
		}
	}
	return m
}

// ServeHandler implements middleware interface
func (m *RateLimit) ServeHandler(h http.Handler) http.Handler {
	if m.Key == nil || m.Rate <= 0 || m.Unit <= 0 {
		return h
	}

	if m.bucket == nil {
		m.bucket = lru.New(m.MaxEntries)
	}

	if m.ExceedHandler == nil {
		m.ExceedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := m.Key(r) + strconv.FormatInt(time.Now().UnixNano()/int64(m.Unit), 10)

		m.mu.RLock()
		currentInf, _ := m.bucket.Get(key)
		m.mu.RUnlock()

		current, _ := currentInf.(int)
		if current > m.Rate {
			m.ExceedHandler.ServeHTTP(w, r)
			return
		}

		m.mu.Lock()
		m.bucket.Add(key, current+1)
		m.mu.Unlock()

		h.ServeHTTP(w, r)
	})
}
