package headers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildHeaders(t *testing.T) {
	t.Run("Nil", func(t *testing.T) {
		hs := buildHeaders(nil)
		assert.Len(t, hs, 0)
	})

	t.Run("1 Pair", func(t *testing.T) {
		hs := buildHeaders([]string{"a", "b"})
		if assert.Len(t, hs, 1) {
			assert.Equal(t, "a", hs[0].Key)
			assert.Equal(t, "b", hs[0].Value)
		}
	})

	t.Run("Odd Args", func(t *testing.T) {
		hs := buildHeaders([]string{"a", "b", "c"})
		if assert.Len(t, hs, 1) {
			assert.Equal(t, "a", hs[0].Key)
			assert.Equal(t, "b", hs[0].Value)
		}
	})

	t.Run("Even Args", func(t *testing.T) {
		hs := buildHeaders([]string{"a", "b", "c", "d"})
		if assert.Len(t, hs, 2) {
			assert.Equal(t, "a", hs[0].Key)
			assert.Equal(t, "b", hs[0].Value)
			assert.Equal(t, "c", hs[1].Key)
			assert.Equal(t, "d", hs[1].Value)
		}
	})
}
