package headers

import (
	"net/http"
	"net/textproto"
	"strings"
)

// MapRequest creates new request mapper
func MapRequest(header string, mapper func(string) string) *RequestMapper {
	return &RequestMapper{
		Header: header,
		Mapper: mapper,
	}
}

// MapGCPHLBImmediateIP extracts client ip from gcp hlb
func MapGCPHLBImmediateIP(proxy int) *RequestMapper {
	return &RequestMapper{
		Header: "X-Forwarded-For",
		Mapper: func(s string) string {
			xs := strings.Split(s, ", ")
			if len(xs) < 2+proxy {
				return s
			}
			return xs[len(xs)-2-proxy]
		},
	}
}

// RequestMapper maps a request's header value
type RequestMapper struct {
	Header string
	Mapper func(string) string
}

// ServeHandler implements middleware interface
func (m *RequestMapper) ServeHandler(h http.Handler) http.Handler {
	if m.Header == "" || m.Mapper == nil {
		return h
	}

	key := textproto.CanonicalMIMEHeaderKey(m.Header)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i, v := range r.Header[key] {
			r.Header[key][i] = m.Mapper(v)
		}

		h.ServeHTTP(w, r)
	})
}

// MapResponse creates new response mapper
func MapResponse(header string, mapper func(string) string) *ResponseMapper {
	return &ResponseMapper{
		Header: header,
		Mapper: mapper,
	}
}

// ResponseMapper maps a response header
type ResponseMapper struct {
	Header string
	Mapper func(string) string
}

// ServeHandler implements middleware interface
func (m *ResponseMapper) ServeHandler(h http.Handler) http.Handler {
	if m.Header == "" || m.Mapper == nil {
		return h
	}

	key := textproto.CanonicalMIMEHeaderKey(m.Header)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nw := responseMapperRW{
			ResponseWriter: w,
			mapHeader:      key,
			mapper:         m.Mapper,
		}
		defer nw.mapping()

		h.ServeHTTP(&nw, r)
	})
}

type responseMapperRW struct {
	http.ResponseWriter
	wroteHeader bool
	mapHeader   string
	mapper      func(string) string
}

func (w *responseMapperRW) mapping() {
	if w.wroteHeader {
		return
	}

	h := w.Header()
	hh := h[w.mapHeader]
	if len(hh) == 0 {
		return
	}

	delete(h, w.mapHeader)
	for _, v := range hh {
		x := w.mapper(v)
		if x != "" {
			h[w.mapHeader] = append(h[w.mapHeader], x)
		}
	}
}

func (w *responseMapperRW) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}

	w.mapping()

	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseMapperRW) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(p)
}
