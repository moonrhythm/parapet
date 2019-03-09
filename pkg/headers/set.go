package headers

import "net/http"

// SetRequest creates new request interceptor for set headers
func SetRequest(headerpairs ...string) *RequestInterceptor {
	hs := buildHeaders(headerpairs)

	return InterceptRequest(func(h http.Header) {
		for _, p := range hs {
			h.Set(p.Key, p.Value)
		}
	})
}

// SetResponse creates new response interceptor for set headers
func SetResponse(headerpairs ...string) *ResponseInterceptor {
	hs := buildHeaders(headerpairs)

	return InterceptResponse(func(w http.ResponseWriter, statusCode int) {
		h := w.Header()
		for _, p := range hs {
			h.Set(p.Key, p.Value)
		}
	})
}
