package parapet

import (
	"net"
	"net/http"
	"strconv"
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
	// The header constants are already in canonical form, so we read and write
	// the header map directly to skip CanonicalMIMEHeaderKey on every access.
	// Each write allocates its own []string{value}: downstream middleware
	// (e.g. headers.MapRequest) may mutate header value slices in place, so
	// any shared/global slice would leak across requests.
	h := r.Header

	// TODO: handle compute full forwarded for from server
	if m.ComputeFullForwardedFor {
		remoteIP := parseHost(r.RemoteAddr)
		if p := headerFirst(h, headerXForwardedFor); p == "" {
			h[headerXForwardedFor] = []string{remoteIP}
		} else {
			h[headerXForwardedFor] = []string{p + ", " + remoteIP}
		}
	}

	if headerFirst(h, headerXRealIP) == "" {
		h[headerXRealIP] = []string{firstHost(headerFirst(h, headerXForwardedFor))}
	}

	if headerFirst(h, headerXForwardedProto) == "" {
		if r.TLS == nil {
			h[headerXForwardedProto] = []string{"http"}
		} else {
			h[headerXForwardedProto] = []string{"https"}
		}
	}

	m.Handler.ServeHTTP(w, r)
}

func (m *proxy) distrust(w http.ResponseWriter, r *http.Request) {
	h := r.Header
	remoteIP := parseHost(r.RemoteAddr)
	// Independent slices for XFF and XRI: sharing one would couple in-place
	// mutations of one header to the other within a single request.
	h[headerXForwardedFor] = []string{remoteIP}
	h[headerXRealIP] = []string{remoteIP}

	if r.TLS == nil {
		h[headerXForwardedProto] = []string{"http"}
	} else {
		h[headerXForwardedProto] = []string{"https"}
	}

	m.Handler.ServeHTTP(w, r)
}

// headerFirst returns the first value for a header key without going through
// http.Header.Get's CanonicalMIMEHeaderKey path. Callers must pass an
// already-canonical key.
func headerFirst(h http.Header, key string) string {
	v := h[key]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func parseHost(s string) string {
	host, _, _ := net.SplitHostPort(s)
	return host
}

func firstHost(s string) string {
	if i := strings.Index(s, ","); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func parseCIDRs(xs []string) []*net.IPNet {
	rs := make([]*net.IPNet, 0, len(xs))
	for _, x := range xs {
		_, n, err := net.ParseCIDR(x)
		if err != nil {
			// Misconfigured trust list silently collapsing to an empty set
			// is a security footgun; fail fast at setup time.
			panic("parapet: invalid CIDR " + strconv.Quote(x) + ": " + err.Error())
		}
		rs = append(rs, n)
	}
	return rs
}
