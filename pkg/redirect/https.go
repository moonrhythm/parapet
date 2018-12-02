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

// NewHTTPS creates new https middleware
func NewHTTPS() *HTTPS {
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
		if (m.TrustProxy && r.Header.Get(m.ForwardedProto) == "http") || r.TLS == nil {
			rd(w, r)
			return
		}

		h.ServeHTTP(w, r)
	})
}
