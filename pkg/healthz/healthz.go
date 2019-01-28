package healthz

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/moonrhythm/parapet"
)

// Healthz middleware
type Healthz struct{}

// New creates new healthz
func New() *Healthz {
	return new(Healthz)
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

		p := atomic.LoadInt32(&shutdown)
		if p > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Service Unavailable"))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
}
