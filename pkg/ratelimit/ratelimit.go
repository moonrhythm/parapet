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
	Key             func(r *http.Request) string
	ExceededHandler ExceededHandler
	Strategy        Strategy

	// Observe, if set, is fired on EVERY Take decision with a bounded Event (the Name
	// below and allowed/limited). Unlike ExceededHandler it does NOT replace the
	// response — observability is independent of what the client sees, so merely
	// COUNTING rejections no longer means reimplementing the 429. It runs synchronously
	// on the request goroutine; keep it cheap. nil disables it. See prom.RateLimit.
	Observe ObserveFunc

	// Name is an operator-set, bounded label carried on every Event so several rate
	// limiters are distinguishable in metrics; "" is fine (the prom adapter maps it).
	// NEVER derived from the client key (unbounded cardinality).
	Name string

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
				m.observe(ResultLimited)
				m.ExceededHandler(w, r, m.Strategy.After(key))
				return
			}
			m.observe(ResultAllowed)
			defer m.Strategy.Put(key) // use defer to always put token back when panic
			h.ServeHTTP(w, r)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := m.Key(r)
		if !m.Strategy.Take(key) {
			m.observe(ResultLimited)
			m.ExceededHandler(w, r, m.Strategy.After(key))
			return
		}
		m.observe(ResultAllowed)

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

// observe fires the Observe hook (nil-checked) with a bounded Event carrying the
// limiter Name and result. It is the single fire point for both ServeHandler
// branches, so the allowed/limited accounting can never diverge between them.
func (m RateLimiter) observe(result Result) {
	if m.Observe != nil {
		m.Observe(Event{Name: m.Name, Result: result})
	}
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
