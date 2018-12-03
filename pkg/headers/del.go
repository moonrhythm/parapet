package headers

import "net/http"

// DelRequest deletes request headers
type DelRequest struct {
	Headers []string
}

// NewDelRequest creates new delete request header middleware
func NewDelRequest(headers ...string) *DelRequest {
	return &DelRequest{Headers: headers}
}

// ServeHandler implements middleware interface
func (m *DelRequest) ServeHandler(h http.Handler) http.Handler {
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

// DelResponse deletes response headers
type DelResponse struct {
	Headers []string
}

// NewDelResponse creates new delete response header middleware
func NewDelResponse(headers ...string) *DelResponse {
	return &DelResponse{Headers: headers}
}

// ServeHandler implements middleware interface
func (m *DelResponse) ServeHandler(h http.Handler) http.Handler {
	if len(m.Headers) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&delResponseRW{
			ResponseWriter: w,
			hide:           m.Headers,
		}, r)
	})
}

type delResponseRW struct {
	http.ResponseWriter
	hide        []string
	wroteHeader bool
}

func (w *delResponseRW) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	for _, h := range w.hide {
		w.Header().Del(h)
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *delResponseRW) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(p)
}
