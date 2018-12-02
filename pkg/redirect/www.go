package redirect

import (
	"net/http"
	"strings"
)

// WWW redirects to www
type WWW struct {
	StatusCode int
}

// NewWWW creates new www middleware
func NewWWW() *WWW {
	return &WWW{}
}

// ServeHandler implements middleware interface
func (m *WWW) ServeHandler(h http.Handler) http.Handler {
	if m.StatusCode == 0 {
		m.StatusCode = http.StatusMovedPermanently
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "www.") {
			http.Redirect(w, r, scheme(r)+"://www."+r.Host+r.RequestURI, m.StatusCode)
			return
		}
		h.ServeHTTP(w, r)
	})
}
