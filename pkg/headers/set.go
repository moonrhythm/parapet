package headers

import "net/http"

// SetRequest creates new request interceptor for set headers.
//
// Keys are canonicalized once at construction; the per-request hot path
// writes directly to the header map, skipping CanonicalMIMEHeaderKey and
// the per-call []string{value} allocation that http.Header.Set incurs.
func SetRequest(headerpairs ...string) *RequestInterceptor {
	hs := buildCanonHeaders(headerpairs)

	return InterceptRequest(func(h http.Header) {
		for _, p := range hs {
			h[p.Key] = p.Value
		}
	})
}

// SetResponse creates new response interceptor for set headers.
func SetResponse(headerpairs ...string) *ResponseInterceptor {
	hs := buildCanonHeaders(headerpairs)

	return InterceptResponse(func(w ResponseHeaderWriter) {
		h := w.Header()
		for _, p := range hs {
			h[p.Key] = p.Value
		}
	})
}
