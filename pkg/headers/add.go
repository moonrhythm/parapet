package headers

import "net/http"

// AddRequest creates request adder
func AddRequest(headerpairs ...string) *RequestAdder {
	return &RequestAdder{Headers: buildHeaders(headerpairs)}
}

// RequestAdder adds request headers
type RequestAdder struct {
	Headers []Header
}

// ServeHandler implements middleware interface
func (m *RequestAdder) ServeHandler(h http.Handler) http.Handler {
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

// AddResponse creates new response adder
func AddResponse(headerpairs ...string) *ResponseAdder {
	return &ResponseAdder{Headers: buildHeaders(headerpairs)}
}

// ResponseAdder adds response headers
type ResponseAdder struct {
	Headers []Header
}

// ServeHandler implements middleware interface
func (m *ResponseAdder) ServeHandler(h http.Handler) http.Handler {
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
