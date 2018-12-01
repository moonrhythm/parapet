package redirect

import "net/http"

// HTTPS redirects to https
type HTTPS struct {
	TrustProxy     bool
	StatusCode     int
	ForwardedProto string
}

// ServeHandler implements middleware interface
func (m *HTTPS) ServeHandler(h http.Handler) http.Handler {
	if m.StatusCode == 0 {
		m.StatusCode = http.StatusMovedPermanently
	}
	if m.ForwardedProto == "" {
		m.ForwardedProto = "X-Forwarded-Proto"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil || !m.TrustProxy || r.Header.Get(m.ForwardedProto) != "http" {
			h.ServeHTTP(w, r)
			return
		}

		http.Redirect(w, r, "https://"+r.Host+r.RequestURI, m.StatusCode)
	})
}
