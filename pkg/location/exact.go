package location

import (
	"net/http"
)

// Exact creates new exact matcher
func Exact(pattern string) *Matcher {
	return New(func(r *http.Request) bool {
		return r.URL.Path == pattern
	})
}
