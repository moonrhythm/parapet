package parapet

import (
	"net"
	"net/http"
)

type trustProxy struct {
	Trust bool
}

func (m trustProxy) ServeHandler(h http.Handler) http.Handler {
	p := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") == "" {
			r.Header.Set("X-Forwarded-For", parseHost(r.RemoteAddr))
		}
		if r.Header.Get("X-Forwarded-Proto") == "" {
			if r.TLS == nil {
				r.Header.Set("X-Forwarded-Proto", "http")
			} else {
				r.Header.Set("X-Forwarded-Proto", "https")
			}
		}

		h.ServeHTTP(w, r)
	})

	if m.Trust {
		return p
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Forwarded-Proto")

		p.ServeHTTP(w, r)
	})
}

func parseHost(s string) string {
	host, _, _ := net.SplitHostPort(s)
	return host
}
