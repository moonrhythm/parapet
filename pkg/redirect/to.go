package redirect

import (
	"net/http"
)

// To redirects to target
func To(target string, statusCode int) *Redirector {
	return &Redirector{
		Target:     target,
		StatusCode: statusCode,
	}
}

// Redirector redirects to target
type Redirector struct {
	Target     string
	StatusCode int
}

// ServeHandler implements middleware interface
func (m Redirector) ServeHandler(h http.Handler) http.Handler {
	if m.StatusCode <= 0 {
		m.StatusCode = http.StatusMovedPermanently
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, m.Target, m.StatusCode)
	})
}
