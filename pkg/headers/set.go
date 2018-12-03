package headers

import "net/http"

// SetRequest sets request headers
type SetRequest struct {
	Headers []Header
}

// NewSetRequest creates new set request header middleware
func NewSetRequest(headerpairs ...string) *SetRequest {
	return &SetRequest{Headers: buildHeaders(headerpairs)}
}

// ServeHandler implements middleware interface
func (m *SetRequest) ServeHandler(h http.Handler) http.Handler {
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

// SetResponse sets response headers
type SetResponse struct {
	Headers []Header
}

// NewSetResponse creates new add response header middleware
func NewSetResponse(headerpairs ...string) *SetResponse {
	return &SetResponse{Headers: buildHeaders(headerpairs)}
}

// ServeHandler implements middleware interface
func (m *SetResponse) ServeHandler(h http.Handler) http.Handler {
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
