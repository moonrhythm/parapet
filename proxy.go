package parapet

import (
	"net"
	"net/http"
	"strings"
)

const (
	headerXForwardedFor   = "X-Forwarded-For"
	headerXForwardedProto = "X-Forwarded-Proto"
	headerXRealIP         = "X-Real-Ip"
)

type trustProxy struct {
	ComputeFullForwardedFor bool
}

func (m trustProxy) ServeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO: handle compute full forwarded for from server
		if m.ComputeFullForwardedFor {
			remoteIP := parseHost(r.RemoteAddr)
			if p := r.Header.Get(headerXForwardedFor); p == "" {
				r.Header.Set(headerXForwardedFor, remoteIP)
			} else {
				r.Header.Set(headerXForwardedFor, p+", "+remoteIP)
			}
		}

		if r.Header.Get(headerXRealIP) == "" {
			r.Header.Set(headerXRealIP, firstHost(r.Header.Get(headerXForwardedFor)))
		}

		if r.Header.Get(headerXForwardedProto) == "" {
			if r.TLS == nil {
				r.Header.Set(headerXForwardedProto, "http")
			} else {
				r.Header.Set(headerXForwardedProto, "https")
			}
		}

		h.ServeHTTP(w, r)
	})
}

type untrustProxy struct{}

func (m untrustProxy) ServeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteIP := parseHost(r.RemoteAddr)
		r.Header.Set(headerXForwardedFor, remoteIP)
		r.Header.Set(headerXRealIP, remoteIP)

		if r.TLS == nil {
			r.Header.Set(headerXForwardedProto, "http")
		} else {
			r.Header.Set(headerXForwardedProto, "https")
		}

		h.ServeHTTP(w, r)
	})
}

func parseHost(s string) string {
	host, _, _ := net.SplitHostPort(s)
	return host
}

func firstHost(s string) string {
	i := strings.Index(s, ",")
	if i < 0 {
		return s
	}
	return s[:i]
}
