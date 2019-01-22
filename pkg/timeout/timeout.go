package timeout

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// New creates timeout middleware
func New(timeout time.Duration) Timout {
	return Timout{Timeout: timeout}
}

// Timout sets a write header timeout
type Timout struct {
	Timeout        time.Duration
	TimeoutHandler http.Handler
}

// ServeHandler implements middleware interface
func (m Timout) ServeHandler(h http.Handler) http.Handler {
	if m.Timeout <= 0 {
		return h
	}

	if m.TimeoutHandler == nil {
		m.TimeoutHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		r = r.WithContext(ctx)
		done := make(chan struct{})

		nw := timeoutRW{
			ResponseWriter: w,
			done:           done,
			header:         make(http.Header),
		}
		go func() {
			select {
			case <-time.After(m.Timeout):
				nw.mu.Lock()
				defer nw.mu.Unlock()

				if nw.wroteHeader {
					break
				}
				nw.timeout = true
				cancel()

				m.TimeoutHandler.ServeHTTP(w, r)
			case <-done:
			case <-ctx.Done():
			}
		}()

		h.ServeHTTP(&nw, r)
	})
}

type timeoutRW struct {
	http.ResponseWriter

	mu          sync.Mutex
	done        chan struct{}
	wroteHeader bool
	header      http.Header
	timeout     bool
}

func (w *timeoutRW) Header() http.Header {
	return w.header
}

func (w *timeoutRW) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	close(w.done)

	if w.timeout {
		return
	}

	h := w.ResponseWriter.Header()
	for k, vv := range w.header {
		h[k] = vv
	}

	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *timeoutRW) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.timeout {
		return len(p), nil
	}

	return w.ResponseWriter.Write(p)
}
