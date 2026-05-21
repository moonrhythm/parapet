package headers

import "net/http"

// DeleteRequest creates new request interceptor for delete headers.
//
// Keys are canonicalized once at construction so the per-request path
// can use a direct map delete instead of http.Header.Del, which scans
// the key through CanonicalMIMEHeaderKey on every call.
func DeleteRequest(headers ...string) *RequestInterceptor {
	keys := canonKeys(headers)
	return InterceptRequest(func(h http.Header) {
		for _, k := range keys {
			delete(h, k)
		}
	})
}

// DeleteResponse creates new response interceptor for delete headers.
func DeleteResponse(headers ...string) *ResponseInterceptor {
	keys := canonKeys(headers)
	return InterceptResponse(func(w ResponseHeaderWriter) {
		h := w.Header()
		for _, k := range keys {
			delete(h, k)
		}
	})
}
