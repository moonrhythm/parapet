package redirect

import (
	"net/http"
	"strings"
)

// NonWWW redirects to non-www
type NonWWW struct {
	StatusCode int
}

// NewNonWWW creates new non www middleware
func NewNonWWW() *NonWWW {
	return &NonWWW{}
}

// ServeHandler implements middleware interface
func (m *NonWWW) ServeHandler(h http.Handler) http.Handler {
	if m.StatusCode == 0 {
		m.StatusCode = http.StatusMovedPermanently
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.TrimPrefix(r.Host, "www.")
		if len(host) < len(r.Host) {
			http.Redirect(w, r, scheme(r)+"://"+host+r.RequestURI, m.StatusCode)
			return
		}
		h.ServeHTTP(w, r)
	})
}
