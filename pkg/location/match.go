package location

import (
	"net/http"

	"github.com/moonrhythm/parapet"
)

// Match creates new matcher
func Match(pattern string) *Matcher {
	return &Matcher{Pattern: pattern}
}

// Matcher matchs exact location
type Matcher struct {
	Pattern string
	ms      parapet.Middlewares
}

// Use uses middleware
func (l *Matcher) Use(m parapet.Middleware) {
	l.ms.Use(m)
}

// ServeHandler implements middleware interface
func (l *Matcher) ServeHandler(h http.Handler) http.Handler {
	next := l.ms.ServeHandler(http.NotFoundHandler())

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != l.Pattern {
			h.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}
