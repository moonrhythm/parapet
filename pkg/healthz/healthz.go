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
	Path     string
	Host     bool // allow request with Host header
	ready    int32
	healthy  int32
	shutdown int32
	once     sync.Once
}

// New creates new healthz
func New() *Healthz {
	return &Healthz{
		Path:    "/healthz",
		ready:   1,
		healthy: 1,
	}
}

// SetReady sets ready state
func (m *Healthz) SetReady(ready bool) {
	var val int32
	if ready {
		val = 1
	}
	atomic.StoreInt32(&m.ready, val)
}

// SetLive sets healthy state
func (m *Healthz) Set(healthy bool) {
	var val int32
	if healthy {
		val = 1
	}
	atomic.StoreInt32(&m.healthy, val)
}

// ServeHandler implements middleware interface
func (m *Healthz) ServeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.once.Do(func() {
			if srv, ok := r.Context().Value(parapet.ServerContextKey).(*parapet.Server); ok {
				srv.RegisterOnShutdown(func() {
					atomic.StoreInt32(&m.shutdown, 1)
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

		var ok bool

		if r.URL.Query().Get("ready") != "" {
			localShutdown := atomic.LoadInt32(&m.shutdown)
			localReady := atomic.LoadInt32(&m.ready)
			ok = localShutdown == 0 && localReady > 0
		} else {
			localLive := atomic.LoadInt32(&m.healthy)
			ok = localLive > 0
		}

		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Service Unavailable"))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
}
