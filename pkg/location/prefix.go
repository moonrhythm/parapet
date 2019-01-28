package location

import (
	"net/http"
	"strings"
)

// Prefix creates new prefix matcher
func Prefix(pattern string) *Matcher {
	if pattern == "" {
		return New(nil)
	}

	return New(func(r *http.Request) bool {
		return strings.HasPrefix(r.URL.Path, pattern)
	})
}
