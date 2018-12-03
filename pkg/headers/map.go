package headers

import (
	"net/http"
	"net/textproto"
	"strings"
)

// MapRequest creates new request mapper
func MapRequest(header string, mapper func(string) string) *RequestMapper {
	return &RequestMapper{
		Header: header,
		Mapper: mapper,
	}
}

// MapGCPHLBImmediateIP extracts client ip from gcp hlb
func MapGCPHLBImmediateIP(proxy int) *RequestMapper {
	return &RequestMapper{
		Header: "X-Forwarded-For",
		Mapper: func(s string) string {
			xs := strings.Split(s, ", ")
			if len(xs) < 2+proxy {
				return s
			}
			return xs[len(xs)-2-proxy]
		},
	}
}

// RequestMapper maps a request's header value
type RequestMapper struct {
	Header string
	Mapper func(string) string
}

// ServeHandler implements middleware interface
func (m *RequestMapper) ServeHandler(h http.Handler) http.Handler {
	if m.Header == "" || m.Mapper == nil {
		return h
	}

	key := textproto.CanonicalMIMEHeaderKey(m.Header)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i, v := range r.Header[key] {
			r.Header[key][i] = m.Mapper(v)
		}

		h.ServeHTTP(w, r)
	})
}
