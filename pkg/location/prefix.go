package location

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet/pkg/block"
)

// Prefix creates new prefix location matcher block.
//
// Matching only succeeds on a path-segment boundary: pattern "/admin"
// matches "/admin" and "/admin/anything" but not "/adminxyz".
func Prefix(pattern string) *block.Block {
	if pattern == "" {
		return block.New(nil)
	}

	return block.New(func(r *http.Request) bool {
		p := r.URL.Path
		if !strings.HasPrefix(p, pattern) {
			return false
		}
		if len(p) == len(pattern) {
			return true
		}
		// pattern already ends with "/" → the next char is part of the sub-path
		if pattern[len(pattern)-1] == '/' {
			return true
		}
		// otherwise the next char must be a path separator
		return p[len(pattern)] == '/'
	})
}
