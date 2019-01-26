package headers

import "net/http"

// SetRequest creates new request setter
func SetRequest(headerpairs ...string) RequestSetter {
	return RequestSetter{Headers: buildHeaders(headerpairs)}
}

// RequestSetter sets request headers
type RequestSetter struct {
	Headers []Header
}

// ServeHandler implements middleware interface
func (m RequestSetter) ServeHandler(h http.Handler) http.Handler {
	if len(m.Headers) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range m.Headers {
			r.Header.Set(p.Key, p.Value)
		}

		h.ServeHTTP(w, r)
	})
}

// SetResponse creates new response setter
func SetResponse(headerpairs ...string) ResponseSetter {
	return ResponseSetter{Headers: buildHeaders(headerpairs)}
}

// ResponseSetter sets response headers
type ResponseSetter struct {
	Headers []Header
}

// ServeHandler implements middleware interface
func (m ResponseSetter) ServeHandler(h http.Handler) http.Handler {
	if len(m.Headers) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&responseSetterRW{
			ResponseWriter: w,
			headers:        m.Headers,
		}, r)
	})
}

type responseSetterRW struct {
	http.ResponseWriter
	headers     []Header
	wroteHeader bool
}

func (w *responseSetterRW) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	for _, p := range w.headers {
		w.Header().Set(p.Key, p.Value)
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseSetterRW) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(p)
}
