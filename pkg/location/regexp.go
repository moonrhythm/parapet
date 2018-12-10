package location

import (
	"net/http"
	"regexp"

	"github.com/moonrhythm/parapet"
)

// RegExp creates new RegExp matcher
func RegExp(pattern string) *RegExpMatcher {
	return &RegExpMatcher{Pattern: pattern}
}

// RegExpMatcher matchs location using regexp
type RegExpMatcher struct {
	Pattern string
	ms      parapet.Middlewares
}

// Use uses middleware
func (l *RegExpMatcher) Use(m parapet.Middleware) {
	l.ms.Use(m)
}

// ServeHandler implements middleware interface
func (l *RegExpMatcher) ServeHandler(h http.Handler) http.Handler {
	next := l.ms.ServeHandler(http.NotFoundHandler())

	// catch-all
	if l.Pattern == "" {
		return next
	}

	re := regexp.MustCompile(l.Pattern)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !re.MatchString(r.URL.Path) {
			h.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}
