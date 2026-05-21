package ratelimit

import (
	"bufio"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/moonrhythm/parapet/pkg/header"
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
	ReleaseOnHijacked    bool // release token when hijacked
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
		header.Set(w.Header(), header.RetryAfter, strconv.FormatInt(int64(after/time.Second), 10))
	}
	http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
}

func defaultKey(_ *http.Request) string {
	return ""
}

// ClientIP returns client ip from request
func ClientIP(r *http.Request) string {
	ipStr := header.Get(r.Header, header.XRealIP)
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

	// Fast path: no response-writer wrapping needed. Avoids allocating a
	// release closure and an escaped bool on every request.
	if !m.ReleaseOnWriteHeader && !m.ReleaseOnHijacked {
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := m.Key(r)
		if !m.Strategy.Take(key) {
			m.ExceededHandler(w, r, m.Strategy.After(key))
			return
		}

		// Release state lives inside the responseWriter, which already
		// escapes — avoids allocating a separate closure + bool.
		nw := &responseWriter{
			ResponseWriter:       w,
			strategy:             m.Strategy,
			key:                  key,
			releaseOnWriteHeader: m.ReleaseOnWriteHeader,
			releaseOnHijack:      m.ReleaseOnHijacked,
		}
		defer nw.release() // use defer to always put token back when panic
		h.ServeHTTP(nw, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	strategy Strategy
	key      string

	wroteHeader          bool
	released             bool
	releaseOnWriteHeader bool
	releaseOnHijack      bool
}

func (w *responseWriter) release() {
	if w.released {
		return
	}
	w.released = true
	w.strategy.Put(w.key)
}

func (w *responseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if w.releaseOnWriteHeader {
		w.release()
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
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
	if nw, ok := w.ResponseWriter.(http.Hijacker); ok {
		if w.releaseOnHijack {
			w.release()
		}
		return nw.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
