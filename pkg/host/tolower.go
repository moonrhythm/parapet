package host

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet"
)

// ToLower converts request's host to lowercase
func ToLower() parapet.Middleware {
	return parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Host = strings.ToLower(r.Host)
			h.ServeHTTP(w, r)
		})
	})
}
