package addheaders

import "net/http"

// Upstream adds headers before send to upstream
type Upstream struct {
	Headers []Header
}

// NewUpstream creates new upstream middleware
func NewUpstream(headerpairs ...string) *Upstream {
	return &Upstream{Headers: buildHeaders(headerpairs)}
}

// ServeHandler implements middleware interface
func (m *Upstream) ServeHandler(h http.Handler) http.Handler {
	if len(m.Headers) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range m.Headers {
			r.Header.Add(p.Key, p.Value)
		}

		h.ServeHTTP(w, r)
	})
}
