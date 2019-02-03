package prom_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet"
	. "github.com/moonrhythm/parapet/pkg/prom"
)

func TestNetworks(t *testing.T) {
	t.Parallel()

	t.Run("Not Panics", func(t *testing.T) {
		s := parapet.New()
		assert.NotPanics(t, func() { Networks(s) })
	})
}
