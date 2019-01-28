package location

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet"
)

// Prefix creates new prefix matcher
func Prefix(pattern string) *PrefixMatcher {
	return &PrefixMatcher{Pattern: pattern}
}

// PrefixMatcher matches location using prefix string
type PrefixMatcher struct {
	Pattern string
	ms      parapet.Middlewares
}

// Use uses middleware
func (l *PrefixMatcher) Use(m parapet.Middleware) {
	l.ms.Use(m)
}

// ServeHandler implements middleware interface
func (l PrefixMatcher) ServeHandler(h http.Handler) http.Handler {
	next := l.ms.ServeHandler(http.NotFoundHandler())

	// catch-all
	if l.Pattern == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, l.Pattern) {
			h.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}
