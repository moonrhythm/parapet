package location

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet/pkg/block"
)

// Prefix creates new prefix location matcher block
func Prefix(pattern string) *block.Block {
	if pattern == "" {
		return block.New(nil)
	}

	return block.New(func(r *http.Request) bool {
		return strings.HasPrefix(r.URL.Path, pattern)
	})
}
