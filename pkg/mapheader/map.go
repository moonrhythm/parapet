package mapheader

import (
	"net/http"
	"net/textproto"
	"strings"
)

// Upstream maps a request's header value
type Upstream struct {
	Header    string
	Extractor func(string) string
}

// ServeHandler implements middleware interface
func (m *Upstream) ServeHandler(h http.Handler) http.Handler {
	if m.Header == "" || m.Extractor == nil {
		return h
	}

	key := textproto.CanonicalMIMEHeaderKey(m.Header)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i, v := range r.Header[key] {
			r.Header[key][i] = m.Extractor(v)
		}

		h.ServeHTTP(w, r)
	})
}

// GCPHLBImmediateIP extracts client ip from gcp hlb
func GCPHLBImmediateIP(proxy int) *Upstream {
	return &Upstream{
		Header: "X-Forwarded-For",
		Extractor: func(s string) string {
			xs := strings.Split(s, ", ")
			if len(xs) < 2+proxy {
				return s
			}
			return xs[len(xs)-2-proxy]
		},
	}
}
