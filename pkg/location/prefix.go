package location

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet"
)

// Prefix matchs location using prefix string
type Prefix struct {
	Pattern string
	ms      parapet.Middlewares
}

// NewPrefix creates new prefix matcher
func NewPrefix(pattern string) *Prefix {
	return &Prefix{Pattern: pattern}
}

// Use uses middleware
func (l *Prefix) Use(m parapet.Middleware) {
	if m == nil {
		return
	}
	l.ms = append(l.ms, m)
}

// ServeHandler implements middleware interface
func (l *Prefix) ServeHandler(h http.Handler) http.Handler {
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
