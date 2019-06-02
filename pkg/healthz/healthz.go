package healthz

import (
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/moonrhythm/parapet"
)

// Healthz middleware
type Healthz struct {
	Path string
	Host bool // allow request with Host header
}

// New creates new healthz
func New() *Healthz {
	return &Healthz{
		Path: "/healthz",
	}
}

// ServeHandler implements middleware interface
func (m Healthz) ServeHandler(h http.Handler) http.Handler {
	var (
		once     sync.Once
		shutdown int32
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			if srv, ok := r.Context().Value(parapet.ServerContextKey).(*parapet.Server); ok {
				srv.RegisterOnShutdown(func() {
					atomic.StoreInt32(&shutdown, 1)
				})
			}
		})

		if !m.Host {
			requestHost, _, _ := net.SplitHostPort(r.Host)
			if requestHost == "" {
				requestHost = r.Host
			}
			ip := net.ParseIP(requestHost)
			if ip == nil {
				h.ServeHTTP(w, r)
				return
			}
		}

		if r.URL.Path != m.Path {
			h.ServeHTTP(w, r)
			return
		}

		if r.URL.Query().Get("ready") != "" {
			p := atomic.LoadInt32(&shutdown)
			if p > 0 {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("Service Unavailable"))
				return
			}
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
}
