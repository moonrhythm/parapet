package headers

import "net/http"

// AddRequest creates new request interceptor for add headers
func AddRequest(headerpairs ...string) *RequestInterceptor {
	hs := buildHeaders(headerpairs)

	return InterceptRequest(func(h http.Header) {
		for _, p := range hs {
			h.Add(p.Key, p.Value)
		}
	})
}

// AddResponse creates new response interceptor for add headers
func AddResponse(headerpairs ...string) *ResponseInterceptor {
	hs := buildHeaders(headerpairs)

	return InterceptResponse(func(w http.ResponseWriter, statusCode int) {
		h := w.Header()
		for _, p := range hs {
			h.Add(p.Key, p.Value)
		}
	})
}
