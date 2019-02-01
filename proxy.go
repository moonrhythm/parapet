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

type proxy struct {
	Trust                   []*net.IPNet
	ComputeFullForwardedFor bool
	Handler                 http.Handler
}

func (m *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(m.Trust) == 0 {
		m.distrust(w, r)
		return
	}

	remoteIP := net.ParseIP(parseHost(r.RemoteAddr))
	if remoteIP == nil {
		m.distrust(w, r)
		return
	}

	for _, p := range m.Trust {
		if p.Contains(remoteIP) {
			m.trust(w, r)
			return
		}
	}

	m.distrust(w, r)
}

func (m *proxy) trust(w http.ResponseWriter, r *http.Request) {
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

	m.Handler.ServeHTTP(w, r)
}

func (m *proxy) distrust(w http.ResponseWriter, r *http.Request) {
	remoteIP := parseHost(r.RemoteAddr)
	r.Header.Set(headerXForwardedFor, remoteIP)
	r.Header.Set(headerXRealIP, remoteIP)

	if r.TLS == nil {
		r.Header.Set(headerXForwardedProto, "http")
	} else {
		r.Header.Set(headerXForwardedProto, "https")
	}

	m.Handler.ServeHTTP(w, r)
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

func parseCIDRs(xs []string) []*net.IPNet {
	var rs []*net.IPNet
	for _, x := range xs {
		_, n, err := net.ParseCIDR(x)
		if err != nil {
			rs = append(rs, n)
		}
	}
	return rs
}
