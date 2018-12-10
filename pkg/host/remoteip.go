package host

import (
	"net"
	"net/http"

	"github.com/moonrhythm/parapet"
)

// RemoteIP creates new remote ip host block
func RemoteIP() parapet.Block {
	return new(RemoteIPBlock)
}

// RemoteIPBlock type
type RemoteIPBlock struct {
	ms parapet.Middlewares
}

// Use uses middleware
func (host *RemoteIPBlock) Use(m parapet.Middleware) {
	host.ms.Use(m)
}

// ServeHandler implements middleware interface
func (host *RemoteIPBlock) ServeHandler(h http.Handler) http.Handler {
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
