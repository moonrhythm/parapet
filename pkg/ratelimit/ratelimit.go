package ratelimit

import (
	"net"
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
	timer  *time.Timer
	last   time.Time
}

// New creates new default rate limiter
func New(ratePerSecond int, trustProxy bool) *RateLimit {
	m := &RateLimit{
		Key: func(r *http.Request) string {
			return parseHost(r.RemoteAddr)
		},
		Rate:       ratePerSecond,
		Unit:       time.Second,
		MaxEntries: 100000,
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

// ServeHandler implements middleware interface
func (m *RateLimit) ServeHandler(h http.Handler) http.Handler {
	if m.Key == nil || m.Rate <= 0 || m.Unit <= 0 {
		return h
	}

	if m.bucket == nil {
		m.bucket = lru.New(m.MaxEntries)
	}

	if m.ExceedHandler == nil {
		delay := strconv.FormatInt(int64(m.Unit/time.Second), 10)
		m.ExceedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", delay)
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		})
	}

	if m.timer == nil {
		d := time.Minute
		m.timer = time.NewTimer(d)
		go func() {
			for {
				<-m.timer.C
				m.timer.Reset(d)

				m.mu.RLock()
				shouldClear := time.Since(m.last) > m.Unit
				m.mu.RUnlock()
				if shouldClear {
					m.mu.Lock()
					m.bucket.Clear()
					m.mu.Unlock()
				}
			}
		}()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		key := m.Key(r) + strconv.FormatInt(now.UnixNano()/int64(m.Unit), 10)

		m.mu.RLock()
		currentInf, _ := m.bucket.Get(key)
		m.mu.RUnlock()

		current, _ := currentInf.(int)
		if current > m.Rate {
			m.ExceedHandler.ServeHTTP(w, r)
			return
		}

		m.mu.Lock()
		m.last = now
		currentInf, _ = m.bucket.Get(key)
		current, _ = currentInf.(int)
		m.bucket.Add(key, current+1)
		m.mu.Unlock()

		h.ServeHTTP(w, r)
	})
}

func parseHost(s string) string {
	host, _, _ := net.SplitHostPort(s)
	return host
}
