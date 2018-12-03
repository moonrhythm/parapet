package headers

import "net/http"

// SetRequest creates new request setter
func SetRequest(headerpairs ...string) *RequestSetter {
	return &RequestSetter{Headers: buildHeaders(headerpairs)}
}

// RequestSetter sets request headers
type RequestSetter struct {
	Headers []Header
}

// ServeHandler implements middleware interface
func (m *RequestSetter) ServeHandler(h http.Handler) http.Handler {
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

// SetResponse creates new response setter
func SetResponse(headerpairs ...string) *ResponseSetter {
	return &ResponseSetter{Headers: buildHeaders(headerpairs)}
}

// ResponseSetter sets response headers
type ResponseSetter struct {
	Headers []Header
}

// ServeHandler implements middleware interface
func (m *ResponseSetter) ServeHandler(h http.Handler) http.Handler {
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
