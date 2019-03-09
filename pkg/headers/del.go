package headers

import "net/http"

// DeleteRequest creates new request interceptor for delete headers
func DeleteRequest(headers ...string) *RequestInterceptor {
	return InterceptRequest(func(h http.Header) {
		for _, p := range headers {
			h.Del(p)
		}
	})
}

// DeleteResponse creates new response interceptor for delete headers
func DeleteResponse(headers ...string) *ResponseInterceptor {
	return InterceptResponse(func(w ResponseHeaderWriter) {
		h := w.Header()
		for _, p := range headers {
			h.Del(p)
		}
	})
}
