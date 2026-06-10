package ratelimit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/moonrhythm/parapet/pkg/ratelimit"
)

func TestFixedWindow(t *testing.T) {
	t.Parallel()

	m := FixedWindow(2, time.Second)
	assert.IsType(t, &FixedWindowStrategy{}, m.Strategy)
}

func TestFixedWindowPerSecond(t *testing.T) {
	t.Parallel()

	m := FixedWindowPerSecond(2)
	assert.IsType(t, &FixedWindowStrategy{}, m.Strategy)
}

func TestFixedWindowPerMinute(t *testing.T) {
	t.Parallel()

	m := FixedWindowPerMinute(2)
	assert.IsType(t, &FixedWindowStrategy{}, m.Strategy)
}

func TestFixedWindowPerHour(t *testing.T) {
	t.Parallel()

	m := FixedWindowPerHour(2)
	assert.IsType(t, &FixedWindowStrategy{}, m.Strategy)
}

// TestFixedWindowAfterMatchesTakeGrid: After must compute the reset on the same
// Unix-epoch grid Take buckets on. Size=7s makes the grids distinguishable:
// time.Truncate's year-1 grid is offset by 62135596800 mod 7 = 4s from the epoch
// grid, so the old Truncate-based After is wrong at EVERY instant for this Size —
// any in-bounds result proves the grids now agree.
func TestFixedWindowAfterMatchesTakeGrid(t *testing.T) {
	t.Parallel()

	const size = 7 * time.Second

	for attempt := 0; ; attempt++ {
		// Fresh strategy per attempt: a retry starts in a new window, where the
		// previous attempt's half-consumed state would invert the Take results.
		s := &FixedWindowStrategy{Max: 1, Size: size}

		before := time.Now()
		ok1 := s.Take("k")
		ok2 := s.Take("k")
		got := s.After("k")
		after := time.Now()

		// Re-stage if a 7s boundary fell inside the bracket — the second Take then
		// legitimately lands in a fresh window and After legitimately reports 0, so
		// no assertion below is meaningful. A retry starts just past the boundary,
		// so it cannot cross again.
		if before.UnixNano()/int64(size) != after.UnixNano()/int64(size) {
			require.Less(t, attempt, 3, "the window boundary kept crossing the bracket")
			continue
		}

		require.True(t, ok1)
		require.False(t, ok2, "budget of 1 is spent within the same window")

		// The true reset on Take's grid, bracketed by the two clock reads: After ran
		// at some instant within [before, after], so its result must lie within the
		// matching interval. The 4s-offset Truncate grid can never land in it.
		nextWindow := (before.UnixNano()/int64(size) + 1) * int64(size)
		assert.Positive(t, got, "blocked key never reports 0 within the window")
		assert.GreaterOrEqual(t, got, time.Duration(nextWindow-after.UnixNano()))
		assert.LessOrEqual(t, got, time.Duration(nextWindow-before.UnixNano()))
		break
	}
}
