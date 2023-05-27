package redirect

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet/pkg/header"
)

// WWW creates new www redirector
func WWW() *WWWRedirector {
	return new(WWWRedirector)
}

// WWWRedirector redirects to www
type WWWRedirector struct {
	StatusCode int
}

// ServeHandler implements middleware interface
func (m WWWRedirector) ServeHandler(h http.Handler) http.Handler {
	if m.StatusCode <= 0 {
		m.StatusCode = http.StatusMovedPermanently
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "www.") {
			proto := header.Get(r.Header, header.XForwardedProto)
			http.Redirect(w, r, proto+"://www."+r.Host+r.RequestURI, m.StatusCode)
			return
		}
		h.ServeHTTP(w, r)
	})
}
