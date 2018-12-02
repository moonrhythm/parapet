package hideheaders

import "net/http"

// Upstream hides upstream headers from client
type Upstream struct {
	Headers []string
}

// NewUpstream creates new upstream middleware
func NewUpstream(headers ...string) *Upstream {
	return &Upstream{Headers: headers}
}

// ServeHandler implements middleware interface
func (m *Upstream) ServeHandler(h http.Handler) http.Handler {
	if len(m.Headers) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range m.Headers {
			r.Header.Del(h)
		}

		h.ServeHTTP(w, r)
	})
}
