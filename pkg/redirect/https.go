package redirect

import (
	"net/http"
)

// HTTPS creates new https redirector
func HTTPS() *HTTPSRedirector {
	return &HTTPSRedirector{
		StatusCode: http.StatusMovedPermanently,
	}
}

// HTTPSRedirector redirects to https
type HTTPSRedirector struct {
	StatusCode int
}

// ServeHandler implements middleware interface
func (m HTTPSRedirector) ServeHandler(h http.Handler) http.Handler {
	if m.StatusCode == 0 {
		m.StatusCode = http.StatusMovedPermanently
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto := r.Header.Get("X-Forwarded-Proto")
		if proto == "http" {
			http.Redirect(w, r, "https://"+r.Host+r.RequestURI, m.StatusCode)
			return
		}

		h.ServeHTTP(w, r)
	})
}
