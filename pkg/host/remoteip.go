package host

import (
	"net"
	"net/http"

	"github.com/moonrhythm/parapet"
)

// NewRemoteIP creates new remote ip
func NewRemoteIP() *RemoteIP {
	return new(RemoteIP)
}

// RemoteIP middleware
type RemoteIP struct {
	ms parapet.Middlewares
}

// Use uses middleware
func (host *RemoteIP) Use(m parapet.Middleware) {
	if m == nil {
		return
	}
	host.ms = append(host.ms, m)
}

// ServeHandler implements middleware interface
func (host *RemoteIP) ServeHandler(h http.Handler) http.Handler {
	next := host.ms.ServeHandler(http.NotFoundHandler())

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteHost, _, _ := net.SplitHostPort(r.RemoteAddr)
		requestHost, _, _ := net.SplitHostPort(r.Host)
		if remoteHost != requestHost {
			h.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}
