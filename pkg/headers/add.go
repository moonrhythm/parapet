package headers

import "net/http"

// AddRequest adds request headers
type AddRequest struct {
	Headers []Header
}

// NewAddRequest creates new add request header middleware
func NewAddRequest(headerpairs ...string) *AddRequest {
	return &AddRequest{Headers: buildHeaders(headerpairs)}
}

// ServeHandler implements middleware interface
func (m *AddRequest) ServeHandler(h http.Handler) http.Handler {
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

// AddResponse adds response headers
type AddResponse struct {
	Headers []Header
}

// NewAddResponse creates new add response header middleware
func NewAddResponse(headerpairs ...string) *AddResponse {
	return &AddResponse{Headers: buildHeaders(headerpairs)}
}

// ServeHandler implements middleware interface
func (m *AddResponse) ServeHandler(h http.Handler) http.Handler {
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
