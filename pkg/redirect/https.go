package redirect

import (
	"net/http"
)

// HTTPS creates new https redirector
func HTTPS() *HTTPSRedirector {
	return &HTTPSRedirector{
		TrustProxy: true,
		StatusCode: http.StatusMovedPermanently,
	}
}

// HTTPSRedirector redirects to https
type HTTPSRedirector struct {
	TrustProxy     bool
	StatusCode     int
	ForwardedProto string
}

// ServeHandler implements middleware interface
func (m *HTTPSRedirector) ServeHandler(h http.Handler) http.Handler {
	if m.StatusCode == 0 {
		m.StatusCode = http.StatusMovedPermanently
	}
	if m.ForwardedProto == "" {
		m.ForwardedProto = "X-Forwarded-Proto"
	}

	rd := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://"+r.Host+r.RequestURI, m.StatusCode)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto := r.Header.Get(m.ForwardedProto)
		if m.TrustProxy && proto != "" {
			if proto == "http" {
				rd(w, r)
				return
			}

			h.ServeHTTP(w, r)
			return
		}

		if r.TLS == nil {
			rd(w, r)
			return
		}

		h.ServeHTTP(w, r)
	})
}
