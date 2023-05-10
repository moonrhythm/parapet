package parapet

import (
	"net"
	"net/http"
	"strings"
)

// TrustCIDRs trusts given CIDR list
func TrustCIDRs(s []string) Conditional {
	trust := parseCIDRs(s)
	if len(trust) == 0 {
		return func(r *http.Request) bool {
			return false
		}
	}

	return func(r *http.Request) bool {
		remoteIP := net.ParseIP(parseHost(r.RemoteAddr))
		if remoteIP == nil {
			return false
		}

		for _, p := range trust {
			if p.Contains(remoteIP) {
				return true
			}
		}
		return false
	}
}

// Trusted trusts all remotes
func Trusted() Conditional {
	return func(r *http.Request) bool {
		return true
	}
}

const (
	headerXForwardedFor   = "X-Forwarded-For"
	headerXForwardedProto = "X-Forwarded-Proto"
	headerXRealIP         = "X-Real-Ip"
)

//nolint:govet
type proxy struct {
	Trust                   func(r *http.Request) bool
	ComputeFullForwardedFor bool
	Handler                 http.Handler
}

func (m *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.Trust == nil {
		m.distrust(w, r)
		return
	}

	if m.Trust(r) {
		m.trust(w, r)
		return
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
		_, n, _ := net.ParseCIDR(x)
		if n != nil {
			rs = append(rs, n)
		}
	}
	return rs
}
