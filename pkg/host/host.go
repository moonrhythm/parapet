package host

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet"
)

// New creates new host middleware
func New(host ...string) Host {
	return Host{Hosts: host}
}

// Host middleware
type Host struct {
	Hosts []string
	ms    parapet.Middlewares
}

// Use uses middleware
func (host *Host) Use(m parapet.Middleware) {
	host.ms.Use(m)
}

// ServeHandler implements middleware interface
func (host Host) ServeHandler(h http.Handler) http.Handler {
	next := host.ms.ServeHandler(http.NotFoundHandler())

	// build host map
	hostMap := make(map[string]bool)
	for _, x := range host.Hosts {
		hostMap[x] = true
	}

	// catch-all
	if len(host.Hosts) == 0 || hostMap["*"] {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// exact match
		if hostMap[r.Host] {
			next.ServeHTTP(w, r)
			return
		}

		// wildcard subdomains
		if i := strings.Index(r.Host, "."); i > 0 && hostMap["*"+r.Host[i:]] {
			next.ServeHTTP(w, r)
			return
		}

		h.ServeHTTP(w, r)
	})
}
