package headers

import "net/http"

// InterceptRequest creates new request interceptor
func InterceptRequest(f func(http.Header)) RequestInterceptor {
	return RequestInterceptor{Intercept: f}
}

// RequestInterceptor intercepts request's headers
type RequestInterceptor struct {
	Intercept func(http.Header)
}

// ServeHandler implements middleware interface
func (m RequestInterceptor) ServeHandler(h http.Handler) http.Handler {
	if m.Intercept == nil {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Intercept(r.Header)
		h.ServeHTTP(w, r)
	})
}

// InterceptResponse creates new response interceptor
func InterceptResponse(f func(http.Header)) ResponseInterceptor {
	return ResponseInterceptor{Intercept: f}
}

// ResponseInterceptor intercepts response's headers
type ResponseInterceptor struct {
	Intercept func(http.Header)
}

// ServeHandler implements middleware interface
func (m ResponseInterceptor) ServeHandler(h http.Handler) http.Handler {
	if m.Intercept == nil {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nw := interceptRW{
			ResponseWriter: w,
			f:              m.Intercept,
		}
		defer nw.intercept()

		h.ServeHTTP(&nw, r)
	})
}

type interceptRW struct {
	http.ResponseWriter
	wroteHeader bool
	f           func(http.Header)
}

func (w *interceptRW) intercept() {
	if w.wroteHeader {
		return
	}
	w.f(w.Header())
}

func (w *interceptRW) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.intercept()
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *interceptRW) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(p)
}
