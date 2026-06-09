package ratelimit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/moonrhythm/parapet/pkg/ratelimit"
)

func TestConcurrentQueue(t *testing.T) {
	t.Parallel()

	m := ConcurrentQueue(0, 0)
	assert.IsType(t, &ConcurrentQueueStrategy{}, m.Strategy)
}

// recvBool waits for a queued Take to return, failing on timeout. Synchronizing on
// the goroutine's actual return (rather than a fixed sleep) is what keeps the
// QueueCount bookkeeping race-free: by the time the value arrives, Take has fully
// returned and decremented QueueCount, so the next Take cannot observe a stale count.
func recvBool(t *testing.T, ch <-chan bool) bool {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the queued Take to return")
		return false
	}
}

// stillBlocked asserts a queued Take has not returned yet.
func stillBlocked(t *testing.T, ch <-chan bool) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("the queued Take returned before a slot was freed")
	default:
	}
}

func TestConcurrentQueueStrategy(t *testing.T) {
	t.Parallel()

	t.Run("After zero", func(t *testing.T) {
		s := ConcurrentQueueStrategy{}
		assert.EqualValues(t, 0, s.After(""))
	})

	t.Run("Take then Put", func(t *testing.T) {
		// Capacity 2 (in-process), queue Size 1: a 3rd concurrent Take queues; a 4th
		// drops; a Put frees a slot and the queued Take acquires it.
		s := ConcurrentQueueStrategy{Capacity: 2, Size: 1}

		require.True(t, s.Take("")) // 1/2 in process
		require.True(t, s.Take("")) // 2/2 in process (capacity full)

		// A 3rd Take queues and blocks until a slot frees.
		q := make(chan bool, 1)
		go func() { q <- s.Take("") }()
		// Let the queued goroutine reach the block (increment QueueCount to 1). There
		// is no external signal for "now blocked"; a generous wait covers CI load.
		time.Sleep(100 * time.Millisecond)

		require.False(t, s.Take(""), "queue full (Size=1) -> drop")
		require.False(t, s.Take(""), "still dropping")
		stillBlocked(t, q)
		require.True(t, s.Take("other"), "a different key is independent")

		s.Put("") // free a slot -> unblock the queued Take
		require.True(t, recvBool(t, q), "the queued Take acquired the freed slot")

		// Repeat: another queued Take, again released by a Put.
		go func() { q <- s.Take("") }()
		time.Sleep(100 * time.Millisecond)
		require.True(t, s.Take("other"), "a different key is still independent")
		s.Put("")
		require.True(t, recvBool(t, q), "the second queued Take acquired its slot")

		s.Put("") // drain back to one in process
		require.True(t, s.Take(""), "a free slot admits immediately")
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
