package ratelimit_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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

// TestSlidingWindowSuppressesBoundaryBurst lives in slidingwindow_internal_test.go:
// the real-clock version here raced 100ms wall-clock windows against the scheduler
// (a ~100ms stall mid-loop straddled a boundary and broke both phases); the internal
// version seeds the rolled state deterministically and still drives the public Take.

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
