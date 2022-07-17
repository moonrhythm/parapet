package ratelimit

import (
	"bufio"
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
	Key                  func(r *http.Request) string
	ExceededHandler      ExceededHandler
	Strategy             Strategy
	ReleaseOnWriteHeader bool // release token when write response's header
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

func defaultExceededHandler(w http.ResponseWriter, _ *http.Request, after time.Duration) {
	if after > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(after/time.Second), 10))
	}
	http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
}

func defaultKey(_ *http.Request) string {
	return ""
}

// ClientIP returns client ip from request
func ClientIP(r *http.Request) string {
	ipStr := r.Header.Get("X-Real-Ip")
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ipStr
	}
	return string(ip)
}

// ServeHandler implements middleware interface
func (m RateLimiter) ServeHandler(h http.Handler) http.Handler {
	if m.Key == nil {
		m.Key = defaultKey
	}
	if m.ExceededHandler == nil {
		m.ExceededHandler = defaultExceededHandler
	}

	if m.ReleaseOnWriteHeader {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := m.Key(r)
			if !m.Strategy.Take(key) {
				m.ExceededHandler(w, r, m.Strategy.After(key))
				return
			}
			release := func() {
				m.Strategy.Put(key)
			}
			nw := responseWriter{
				ResponseWriter: w,
				OnWriteHeader:  release,
			}
			defer func() {
				if nw.wroteHeader {
					return
				}
				release()
			}()

			h.ServeHTTP(&nw, r)
		})
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

type responseWriter struct {
	http.ResponseWriter
	OnWriteHeader func()
	wroteHeader   bool
}

func (w *responseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(statusCode)
	w.OnWriteHeader()
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

// Push implements Pusher interface
func (w *responseWriter) Push(target string, opts *http.PushOptions) error {
	if w, ok := w.ResponseWriter.(http.Pusher); ok {
		return w.Push(target, opts)
	}
	return http.ErrNotSupported
}

// Flush implements Flusher interface
func (w *responseWriter) Flush() {
	if w, ok := w.ResponseWriter.(http.Flusher); ok {
		w.Flush()
	}
}

// Hijack implements Hijacker interface
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w, ok := w.ResponseWriter.(http.Hijacker); ok {
		return w.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
