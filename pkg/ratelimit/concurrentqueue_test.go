package ratelimit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/ratelimit"
)

func TestConcurrentQueue(t *testing.T) {
	t.Parallel()

	m := ConcurrentQueue(0, 0)
	assert.IsType(t, &ConcurrentQueueStrategy{}, m.Strategy)
}

func TestConcurrentQueueStrategy(t *testing.T) {
	t.Parallel()

	t.Run("After zero", func(t *testing.T) {
		s := ConcurrentQueueStrategy{}
		assert.EqualValues(t, 0, s.After(""))
	})

	t.Run("Take then Put", func(t *testing.T) {
		s := ConcurrentQueueStrategy{
			Capacity: 2,
			Size:     1,
		}

		assert.True(t, s.Take("")) // capacity = 1, size = 0
		assert.True(t, s.Take("")) // capacity = 2, size = 0
		go func() {
			assert.True(t, s.Take("")) // capacity = 2, size = 1
		}()
		time.Sleep(5 * time.Millisecond)
		assert.False(t, s.Take("")) // capacity = 2, size = 1, drop
		assert.False(t, s.Take("")) // capacity = 2, size = 1, drop
		assert.True(t, s.Take("1"))
		s.Put("") // capacity = 2, size = 0
		time.Sleep(5 * time.Millisecond)
		go func() {
			assert.True(t, s.Take("")) // capacity = 2, size = 1
		}()
		time.Sleep(5 * time.Millisecond)
		assert.True(t, s.Take("1"))
		s.Put("") // capacity = 2, size = 0
		time.Sleep(5 * time.Millisecond)
		s.Put("") // capacity = 1, size = 0
		time.Sleep(5 * time.Millisecond)
		assert.True(t, s.Take("")) // capacity = 2, size = 0
		time.Sleep(5 * time.Millisecond)
	})

	t.Run("Put before take", func(t *testing.T) {
		s := ConcurrentQueueStrategy{
			Capacity: 2,
			Size:     1,
		}

		s.Put("")
		s.Take("")
		s.Put("1")
	})
}
