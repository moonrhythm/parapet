package host

import (
	"net/http"

	"github.com/moonrhythm/parapet"
)

// Host middleware
type Host struct {
	Host string
	ms   parapet.Middlewares
}

// New creates new host
func New(host string) *Host {
	return &Host{Host: host}
}

// Use uses middleware
func (host *Host) Use(m parapet.Middleware) {
	if m == nil {
		return
	}
	host.ms = append(host.ms, m)
}

// ServeHandler implements middleware interface
func (host *Host) ServeHandler(h http.Handler) http.Handler {
	next := host.ms.ServeHandler(http.NotFoundHandler())

	// catch-all
	if host.Host == "" || host.Host == "*" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host.Host {
			h.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}
