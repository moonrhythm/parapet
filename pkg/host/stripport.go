package host

import (
	"net"
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet"
)

// StripPort strips port from request's host
func StripPort() parapet.Middleware {
	return parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Host = stripHostPort(r.Host)
			h.ServeHTTP(w, r)
		})
	})
}

func stripHostPort(h string) string {
	if !strings.Contains(h, ":") {
		return h
	}
	host, _, err := net.SplitHostPort(h)
	if err != nil {
		return h
	}
	return host
}
