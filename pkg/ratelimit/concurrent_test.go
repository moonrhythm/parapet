package ratelimit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/ratelimit"
)

func TestConcurrent(t *testing.T) {
	t.Parallel()

	m := Concurrent(2)
	assert.IsType(t, &ConcurrentStrategy{}, m.Strategy)
}

func TestConcurrentStrategy(t *testing.T) {
	t.Parallel()

	t.Run("After zero", func(t *testing.T) {
		s := ConcurrentStrategy{}
		assert.EqualValues(t, 0, s.After(""))
	})

	t.Run("Take then Put", func(t *testing.T) {
		s := ConcurrentStrategy{
			Capacity: 2,
		}

		assert.True(t, s.Take(""))
		assert.True(t, s.Take(""))
		assert.False(t, s.Take(""))
		assert.False(t, s.Take(""))
		assert.True(t, s.Take("1"))
		s.Put("")
		assert.True(t, s.Take(""))
		assert.True(t, s.Take("1"))
		s.Put("")
		assert.False(t, s.Take("1"))
		s.Put("")
		assert.True(t, s.Take(""))
	})

	t.Run("Put before take", func(t *testing.T) {
		s := ConcurrentStrategy{
			Capacity: 2,
		}

		s.Put("")
		s.Take("")
		s.Put("1")
	})
}
