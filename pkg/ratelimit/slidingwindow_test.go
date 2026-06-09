package ratelimit_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/moonrhythm/parapet/pkg/ratelimit"
)

func TestSlidingWindow(t *testing.T) {
	t.Parallel()

	m := SlidingWindow(2, time.Second)
	assert.IsType(t, &SlidingWindowStrategy{}, m.Strategy)
}

func TestSlidingWindowPerSecond(t *testing.T) {
	t.Parallel()

	m := SlidingWindowPerSecond(2)
	assert.IsType(t, &SlidingWindowStrategy{}, m.Strategy)
}

func TestSlidingWindowPerMinute(t *testing.T) {
	t.Parallel()

	m := SlidingWindowPerMinute(2)
	assert.IsType(t, &SlidingWindowStrategy{}, m.Strategy)
}

func TestSlidingWindowPerHour(t *testing.T) {
	t.Parallel()

	m := SlidingWindowPerHour(2)
	assert.IsType(t, &SlidingWindowStrategy{}, m.Strategy)
}

// TestSlidingWindowBurstAdmitsExactlyMax: a burst within a single window (Size huge,
// so no boundary is crossed) admits exactly Max — the steady-state cap.
func TestSlidingWindowBurstAdmitsExactlyMax(t *testing.T) {
	t.Parallel()

	b := &SlidingWindowStrategy{Max: 10, Size: time.Hour}
	admitted := 0
	for range 100 {
		if b.Take("client") {
			admitted++
		}
	}
	assert.Equal(t, 10, admitted, "exactly Max admitted within one window")
	assert.Positive(t, b.After("client"), "blocked -> a positive retry-after")
	assert.Zero(t, b.After("other"), "an unknown key can take now")
}

// TestSlidingWindowSuppressesBoundaryBurst is the integration proof, through the
// public Take across a REAL window roll, that the previous window's spent budget
// suppresses the up-to-2x burst a fixed window admits at its boundary. Timing-loose
// by construction (the assertion tolerates landing anywhere in the first ~90% of the
// next window); the exact smoothing math is pinned deterministically in the internal
// tests.
func TestSlidingWindowSuppressesBoundaryBurst(t *testing.T) {
	t.Parallel()

	const max = 10
	size := 100 * time.Millisecond
	b := &SlidingWindowStrategy{Max: max, Size: size}

	// Land ~2ms into a fresh window so the fill below stays within one window.
	now := time.Now().UnixNano()
	toBoundary := (now/int64(size)+1)*int64(size) - now
	time.Sleep(time.Duration(toBoundary) + 2*time.Millisecond)

	filled := 0
	for range 100 {
		if b.Take("k") {
			filled++
		}
	}
	require.Equal(t, max, filled, "window N admits exactly Max")

	// Cross into window N+1 (prev is now Max) and hammer immediately: a fixed window
	// would grant another fresh Max here; the sliding window must admit far fewer.
	time.Sleep(size)
	burst := 0
	for range 100 {
		if b.Take("k") {
			burst++
		}
	}
	assert.Less(t, burst, max, "the previous window suppresses the boundary burst (no 2x)")
}

// TestSlidingWindowMaxZero: a zero limit admits nothing (the honest reading,
// matching FixedWindow's no-magic-default stance).
func TestSlidingWindowMaxZero(t *testing.T) {
	t.Parallel()

	b := &SlidingWindowStrategy{Max: 0, Size: time.Second}
	assert.False(t, b.Take("client"))
	assert.False(t, b.Take("client"))
}

// TestSlidingWindowSizeZero: a hand-built Size:0 must not divide-by-zero panic (the
// size() guard); only reachable without a constructor.
func TestSlidingWindowSizeZero(t *testing.T) {
	t.Parallel()

	b := &SlidingWindowStrategy{Max: 1, Size: 0}
	assert.NotPanics(t, func() {
		assert.True(t, b.Take("client")) // first admitted
		assert.False(t, b.Take("client"))
		_ = b.After("client")
	})
}

// TestSlidingWindowRace drives concurrent Take and After on the same key across
// frequent boundary crossings: After reads under RLock on local copies, so it must
// neither race nor corrupt Take's counts. A separate-atomics design would race here.
func TestSlidingWindowRace(t *testing.T) {
	t.Parallel()

	b := &SlidingWindowStrategy{Max: 100, Size: 2 * time.Millisecond}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 2000 {
				b.Take("client")
			}
		}()
		go func() {
			defer wg.Done()
			for range 2000 {
				_ = b.After("client")
			}
		}()
	}
	wg.Wait()
}
