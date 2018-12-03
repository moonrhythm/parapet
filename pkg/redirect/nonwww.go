package redirect

import (
	"net/http"
	"strings"
)

// NonWWW redirects to non-www
type NonWWW struct {
	StatusCode int
}

// NonWWWPermanent creates new non www middleware with 301
func NonWWWPermanent() *NonWWW {
	return &NonWWW{
		StatusCode: http.StatusMovedPermanently,
	}
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
