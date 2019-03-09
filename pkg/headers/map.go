package headers

import (
	"net/http"
	"net/textproto"
)

// MapRequest creates new request interceptor for map a header
func MapRequest(header string, mapper func(string) string) *RequestInterceptor {
	header = textproto.CanonicalMIMEHeaderKey(header)

	return InterceptRequest(func(h http.Header) {
		for i, v := range h[header] {
			h[header][i] = mapper(v)
		}
	})
}

// MapResponse creates new response interceptor for map a header
func MapResponse(header string, mapper func(string) string) *ResponseInterceptor {
	header = textproto.CanonicalMIMEHeaderKey(header)

	return InterceptResponse(func(w ResponseHeaderWriter) {
		h := w.Header()
		hh := h[header]
		if len(hh) == 0 {
			return
		}

		delete(h, header)
		for _, v := range hh {
			if x := mapper(v); x != "" {
				h[header] = append(h[header], x)
			}
		}
	})
}
