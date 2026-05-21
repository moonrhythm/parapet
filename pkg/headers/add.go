package headers

import "net/http"

// AddRequest creates new request interceptor for add headers.
//
// Keys are canonicalized once at construction; the per-request path
// appends directly to the header map, skipping CanonicalMIMEHeaderKey
// in http.Header.Add.
func AddRequest(headerpairs ...string) *RequestInterceptor {
	hs := buildHeaders(headerpairs)
	for i := range hs {
		hs[i].Key = http.CanonicalHeaderKey(hs[i].Key)
	}

	return InterceptRequest(func(h http.Header) {
		for _, p := range hs {
			h[p.Key] = append(h[p.Key], p.Value)
		}
	})
}

// AddResponse creates new response interceptor for add headers.
func AddResponse(headerpairs ...string) *ResponseInterceptor {
	hs := buildHeaders(headerpairs)
	for i := range hs {
		hs[i].Key = http.CanonicalHeaderKey(hs[i].Key)
	}

	return InterceptResponse(func(w ResponseHeaderWriter) {
		h := w.Header()
		for _, p := range hs {
			h[p.Key] = append(h[p.Key], p.Value)
		}
	})
}
