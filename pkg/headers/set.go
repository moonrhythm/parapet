package headers

import "net/http"

// SetRequest creates new request interceptor for set headers.
//
// Keys are canonicalized once at construction; the per-request path writes
// directly to the header map, skipping CanonicalMIMEHeaderKey in
// http.Header.Set. A fresh []string{value} is allocated per request — sharing
// a pre-built slice would let downstream in-place mutations (e.g. MapRequest)
// leak across requests.
func SetRequest(headerpairs ...string) *RequestInterceptor {
	hs := buildCanonicalHeaders(headerpairs)

	return InterceptRequest(func(h http.Header) {
		for _, p := range hs {
			h[p.Key] = []string{p.Value}
		}
	})
}

// SetResponse creates new response interceptor for set headers.
func SetResponse(headerpairs ...string) *ResponseInterceptor {
	hs := buildCanonicalHeaders(headerpairs)

	return InterceptResponse(func(w ResponseHeaderWriter) {
		h := w.Header()
		for _, p := range hs {
			h[p.Key] = []string{p.Value}
		}
	})
}
