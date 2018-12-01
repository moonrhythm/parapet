package hideheaders

import "net/http"

// Response hides response headers from client
type Response struct {
	Headers []string
}

// ServeHandler implements middleware interface
func (m *Response) ServeHandler(h http.Handler) http.Handler {
	if len(m.Headers) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&responseWriter{
			ResponseWriter: w,
			hide:           m.Headers,
		}, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	hide        []string
	wroteHeader bool
}

func (w *responseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	for _, h := range w.hide {
		w.Header().Del(h)
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(p)
}
