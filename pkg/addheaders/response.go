package addheaders

import "net/http"

// Response adds response headers before send to client
type Response struct {
	Headers []Header
}

// NewResponse creates new response middleware
func NewResponse(headerpairs ...string) *Response {
	return &Response{Headers: buildHeaders(headerpairs)}
}

// ServeHandler implements middleware interface
func (m *Response) ServeHandler(h http.Handler) http.Handler {
	if len(m.Headers) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range m.Headers {
			w.Header().Add(p.Key, p.Value)
		}

		h.ServeHTTP(w, r)
	})
}
