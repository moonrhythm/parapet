package location

import (
	"net/http"

	"github.com/moonrhythm/parapet/pkg/block"
)

// Exact creates new exact location matcher block
func Exact(pattern string) *block.Block {
	return block.New(func(r *http.Request) bool {
		return r.URL.Path == pattern
	})
}
