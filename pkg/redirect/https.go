package redirect

import (
	"net/http"
)

// HTTPS redirects to https
type HTTPS struct {
	TrustProxy     bool
	StatusCode     int
	ForwardedProto string
}

// HTTPSPermanent creates new https middleware with 301
func HTTPSPermanent() *HTTPS {
	return &HTTPS{
		TrustProxy: true,
		StatusCode: http.StatusMovedPermanently,
	}
}

// ServeHandler implements middleware interface
func (m *HTTPS) ServeHandler(h http.Handler) http.Handler {
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
