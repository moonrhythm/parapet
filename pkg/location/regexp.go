package location

import (
	"net/http"
	"regexp"

	"github.com/moonrhythm/parapet"
)

// RegExp matchs location using regexp
type RegExp struct {
	Pattern string
	ms      parapet.Middlewares
}

// NewRegExp creates new RegExp matcher
func NewRegExp(pattern string) *RegExp {
	return &RegExp{Pattern: pattern}
}

// Use uses middleware
func (l *RegExp) Use(m parapet.Middleware) {
	if m == nil {
		return
	}
	l.ms = append(l.ms, m)
}

// ServeHandler implements middleware interface
func (l *RegExp) ServeHandler(h http.Handler) http.Handler {
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
