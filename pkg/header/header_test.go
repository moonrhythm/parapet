package header_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet/pkg/header"
)

func TestSetShared(t *testing.T) {
	t.Parallel()

	t.Run("sets value", func(t *testing.T) {
		h := http.Header{}
		header.SetShared(h, "X-Test", []string{"v"})
		assert.Equal(t, "v", h.Get("X-Test"))
	})

	t.Run("shares the backing array across calls", func(t *testing.T) {
		vs := []string{"v"}
		h1 := http.Header{}
		h2 := http.Header{}
		header.SetShared(h1, "X-Test", vs)
		header.SetShared(h2, "X-Test", vs)
		// Both maps reference the same slice — no per-call allocation.
		assert.Same(t, &h1["X-Test"][0], &h2["X-Test"][0])
		assert.Same(t, &vs[0], &h1["X-Test"][0])
	})

	t.Run("nil header is a no-op", func(t *testing.T) {
		assert.NotPanics(t, func() {
			header.SetShared(nil, "X-Test", []string{"v"})
		})
	})
}
