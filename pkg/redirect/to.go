package redirect

import (
	"net/http"
)

// To redirects to target
type To struct {
	Target     string
	StatusCode int
}

// ToPermanent redirects to target using 301
func ToPermanent(target string) *To {
	return &To{
		Target:     target,
		StatusCode: http.StatusMovedPermanently,
	}
}

// ToFound redirects to target using 302
func ToFound(target string) *To {
	return &To{
		Target:     target,
		StatusCode: http.StatusFound,
	}
}

// ServeHandler implements middleware interface
func (m *To) ServeHandler(h http.Handler) http.Handler {
	if m.StatusCode == 0 {
		m.StatusCode = http.StatusMovedPermanently
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, m.Target, m.StatusCode)
	})
}
