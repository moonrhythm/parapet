package location

import (
	"net/http"

	"github.com/moonrhythm/parapet"
)

// Exact creates new exact matcher
func Exact(pattern string) ExactMatcher {
	return ExactMatcher{Pattern: pattern}
}

// ExactMatcher matches exact location
type ExactMatcher struct {
	Pattern string
	ms      parapet.Middlewares
}

// Use uses middleware
func (l *ExactMatcher) Use(m parapet.Middleware) {
	l.ms.Use(m)
}

// ServeHandler implements middleware interface
func (l ExactMatcher) ServeHandler(h http.Handler) http.Handler {
	next := l.ms.ServeHandler(http.NotFoundHandler())

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != l.Pattern {
			h.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}
