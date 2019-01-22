package host

import (
	"net"
	"net/http"

	"github.com/moonrhythm/parapet"
)

// NewCIDR creates new CIDR host matcher
func NewCIDR(pattern ...string) CIDR {
	return CIDR{Patterns: pattern}
}

// CIDR matches http host with CIDR
type CIDR struct {
	Patterns []string
	ms       parapet.Middlewares
}

// Use uses middleware
func (host *CIDR) Use(m parapet.Middleware) {
	host.ms.Use(m)
}

// ServeHandler implements middleware interface
func (host CIDR) ServeHandler(h http.Handler) http.Handler {
	next := host.ms.ServeHandler(http.NotFoundHandler())

	var nets []*net.IPNet

	for _, p := range host.Patterns {
		_, n, _ := net.ParseCIDR(p)
		if n != nil {
			nets = append(nets, n)
		}
	}
	if len(nets) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHost, _, _ := net.SplitHostPort(r.Host)
		ip := net.ParseIP(requestHost)
		if ip == nil {
			h.ServeHTTP(w, r)
			return
		}

		for _, n := range nets {
			if !n.Contains(ip) {
				h.ServeHTTP(w, r)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
