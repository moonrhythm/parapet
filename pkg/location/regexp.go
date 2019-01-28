package location

import (
	"net/http"
	"regexp"

	"github.com/moonrhythm/parapet/pkg/block"
)

// RegExp creates new RegExp location matcher block
func RegExp(pattern string) *block.Block {
	if pattern == "" {
		return block.New(nil)
	}

	re := regexp.MustCompile(pattern)

	return block.New(func(r *http.Request) bool {
		return re.MatchString(r.URL.Path)
	})
}
