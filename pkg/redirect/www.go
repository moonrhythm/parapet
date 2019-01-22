package redirect

import (
	"net/http"
	"strings"
)

// WWW creates new www redirector
func WWW() WWWRedirector {
	return WWWRedirector{
		StatusCode: http.StatusMovedPermanently,
	}
}

// WWWRedirector redirects to www
type WWWRedirector struct {
	StatusCode int
}

// ServeHandler implements middleware interface
func (m WWWRedirector) ServeHandler(h http.Handler) http.Handler {
	if m.StatusCode == 0 {
		m.StatusCode = http.StatusMovedPermanently
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "www.") {
			proto := r.Header.Get("X-Forwarded-Proto")
			http.Redirect(w, r, proto+"://www."+r.Host+r.RequestURI, m.StatusCode)
			return
		}
		h.ServeHTTP(w, r)
	})
}
