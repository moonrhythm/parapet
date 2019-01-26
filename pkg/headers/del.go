package headers

import "net/http"

// DeleteRequest creates new request deleter
func DeleteRequest(headers ...string) RequestDeleter {
	return RequestDeleter{Headers: headers}
}

// RequestDeleter deletes request headers
type RequestDeleter struct {
	Headers []string
}

// ServeHandler implements middleware interface
func (m RequestDeleter) ServeHandler(h http.Handler) http.Handler {
	if len(m.Headers) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range m.Headers {
			r.Header.Del(h)
		}

		h.ServeHTTP(w, r)
	})
}

// DeleteResponse creates new response deleter
func DeleteResponse(headers ...string) ResponseDeleter {
	return ResponseDeleter{Headers: headers}
}

// ResponseDeleter deletes response headers
type ResponseDeleter struct {
	Headers []string
}

// ServeHandler implements middleware interface
func (m ResponseDeleter) ServeHandler(h http.Handler) http.Handler {
	if len(m.Headers) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&responseDeleterRW{
			ResponseWriter: w,
			headers:        m.Headers,
		}, r)
	})
}

type responseDeleterRW struct {
	http.ResponseWriter
	headers     []string
	wroteHeader bool
}

func (w *responseDeleterRW) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	for _, h := range w.headers {
		w.Header().Del(h)
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseDeleterRW) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(p)
}
