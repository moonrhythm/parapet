package location

import (
	"net/http"

	"github.com/moonrhythm/parapet"
)

// New creates news location matcher
func New(match func(r *http.Request) bool) *Matcher {
	return &Matcher{
		Match: match,
	}
}

// Matcher is the location matcher
type Matcher struct {
	ms parapet.Middlewares

	Match func(r *http.Request) bool
}

// Use uses middleware
func (matcher *Matcher) Use(m parapet.Middleware) {
	matcher.ms.Use(m)
}

// ServeHandler implements middleware interface
func (matcher Matcher) ServeHandler(h http.Handler) http.Handler {
	next := matcher.ms.ServeHandler(http.NotFoundHandler())

	if matcher.Match == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if matcher.Match(r) {
			next.ServeHTTP(w, r)
			return
		}

		h.ServeHTTP(w, r)
	})
}
