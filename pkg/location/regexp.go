package location

import (
	"net/http"
	"regexp"
)

// RegExp creates new RegExp matcher
func RegExp(pattern string) *Matcher {
	if pattern == "" {
		return New(nil)
	}

	re := regexp.MustCompile(pattern)

	return New(func(r *http.Request) bool {
		return re.MatchString(r.URL.Path)
	})
}
