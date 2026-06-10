package ratelimit

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const swSize = int64(time.Second) // 1e9 ns; frac f maps to now = window*swSize + f*swSize

func swNow(window int64, frac float64) int64 {
	return window*swSize + int64(frac*float64(swSize))
}

// TestSlidingWindowWeightedCount is the boundary-smoothing proof: at the boundary
// (frac 0) the full previous count carries over, so a window that already spent Max
// has ZERO fresh budget (no 2x burst); it fades out linearly as the window elapses.
func TestSlidingWindowWeightedCount(t *testing.T) {
	t.Parallel()

	// prev=10, curr=0: the count fades from 10 (frac 0) to ~0 (frac->1).
	assert.InDelta(t, 10.0, weightedCount(10, 0, swNow(5, 0), swSize), 1e-9, "boundary: full prev counts -> no burst")
	assert.InDelta(t, 5.0, weightedCount(10, 0, swNow(5, 0.5), swSize), 1e-9, "halfway: half the prev budget regained")
	assert.InDelta(t, 2.5, weightedCount(10, 0, swNow(5, 0.75), swSize), 1e-9)

	// curr is never discounted; it adds on top of the fading prev.
	assert.InDelta(t, 13.0, weightedCount(10, 3, swNow(5, 0), swSize), 1e-9)
	assert.InDelta(t, 10.0, weightedCount(0, 10, swNow(5, 0.3), swSize), 1e-9, "no prev -> just curr")

	// The decision the boundary case drives: a window that spent Max denies fresh
	// requests right after the boundary, but regains half its budget at the midpoint.
	const max = 10
	assert.Greater(t, weightedCount(max, 0, swNow(5, 0), swSize)+1, float64(max), "boundary denies fresh")
	assert.LessOrEqual(t, weightedCount(max, 0, swNow(5, 0.5), swSize)+1, float64(max), "midpoint admits again")
}

func TestSlidingWindowRoll(t *testing.T) {
	t.Parallel()

	t.Run("same window or backward clock is a no-op", func(t *testing.T) {
		it := slidingItem{window: 5, curr: 3, prev: 2}
		it.roll(5)
		assert.Equal(t, slidingItem{window: 5, curr: 3, prev: 2}, it, "same window unchanged")
		it.roll(3) // clock stepped backward
		assert.Equal(t, slidingItem{window: 3, curr: 3, prev: 2}, it, "backward step keeps counts, no negative shift")
	})

	t.Run("one window advances curr into prev", func(t *testing.T) {
		it := slidingItem{window: 5, curr: 3, prev: 2}
		it.roll(6)
		assert.Equal(t, slidingItem{window: 6, curr: 0, prev: 3}, it)
	})

	t.Run("two or more windows clears both", func(t *testing.T) {
		it := slidingItem{window: 5, curr: 3, prev: 2}
		it.roll(8)
		assert.Equal(t, slidingItem{window: 8, curr: 0, prev: 0}, it)
	})
}

// TestSlidingWindowAfterAt verifies the closed-form decay solve deterministically
// (an injected now — a wall-clock fixture is nondeterministic).
func TestSlidingWindowAfterAt(t *testing.T) {
	t.Parallel()

	t.Run("under the limit can take now", func(t *testing.T) {
		assert.Zero(t, afterAt(10, 0, 0, swSize, swNow(5, 0.0)))
		assert.Zero(t, afterAt(10, 5, 0, swSize, swNow(5, 0.5))) // weighted 2.5 +1 <= 10
	})

	t.Run("closed-form: prev at limit decays linearly", func(t *testing.T) {
		// prev=10, curr=0, Max=10. A token frees when prev decays to 9, i.e. at frac 0.1.
		assert.InDelta(t, float64(100*time.Millisecond), float64(afterAt(10, 10, 0, swSize, swNow(5, 0.0))), float64(time.Millisecond))
		assert.InDelta(t, float64(50*time.Millisecond), float64(afterAt(10, 10, 0, swSize, swNow(5, 0.05))), float64(time.Millisecond))
	})

	t.Run("full window: wait past the boundary for curr to decay", func(t *testing.T) {
		// prev=0, curr=10, Max=10 at frac 0.3. At the boundary curr becomes the new
		// prev (=10), which still blocks until it decays to 9 at frac 0.1 of the next
		// window: 0.7s to the boundary + 0.1s = 800ms. (NOT 700ms — at 700ms it would
		// be re-denied.)
		got := afterAt(10, 0, 10, swSize, swNow(5, 0.3))
		assert.InDelta(t, float64(800*time.Millisecond), float64(got), float64(time.Millisecond))
	})

	t.Run("never reports 0 while blocked, stays within the window", func(t *testing.T) {
		// Just shy of the freeing fraction: a tiny-but-positive wait that lands before
		// the boundary (the targetFrac<1 gate guarantees it, not a clamp).
		got := afterAt(10, 10, 0, swSize, swNow(5, 0.0999))
		assert.Positive(t, got)
		assert.Less(t, got, time.Duration(swSize)) // strictly within this window
	})

	t.Run("full window waits strictly past the boundary", func(t *testing.T) {
		// curr at Max: relief is past the boundary (curr becomes the next prev, which
		// still blocks until it decays). The wait must EXCEED toBoundary — the rework's
		// +nextFrac term; the original (return toBoundary) was too optimistic here.
		now := swNow(5, 0.0) // boundary: toBoundary == one full window
		got := afterAt(10, 0, 10, swSize, now)
		assert.Greater(t, got, time.Duration(swSize), "waits past the boundary, not merely to it")
		assert.LessOrEqual(t, weightedCount(10, 0, now+int64(got), swSize)+1, float64(10), "admissible at now+wait")
	})
}

// TestSlidingWindowAfterAtNeverTooOptimistic: after waiting afterAt, the request must
// actually be admissible — the reported wait is never too short.
func TestSlidingWindowAfterAtNeverTooOptimistic(t *testing.T) {
	t.Parallel()

	const max = 10
	for _, prev := range []int{0, 3, 7, 10} {
		for _, curr := range []int{0, 2, 9, 10} {
			for f := 0.0; f < 1.0; f += 0.13 {
				now := swNow(5, f)
				wait := afterAt(max, prev, curr, swSize, now)
				if wait == 0 {
					assert.LessOrEqual(t, weightedCount(prev, curr, now, swSize)+1, float64(max),
						"reported 0 only when actually admissible (prev=%d curr=%d f=%.2f)", prev, curr, f)
					continue
				}
				// At now+wait the (rolled) counts must admit a fresh request.
				future := now + int64(wait)
				fp, fc := prev, curr
				if future/swSize > now/swSize { // crossed the boundary: roll curr->prev
					fp, fc = curr, 0
				}
				assert.LessOrEqual(t, weightedCount(fp, fc, future, swSize)+1, float64(max)+1e-6,
					"after the reported wait the request is admissible (prev=%d curr=%d f=%.2f wait=%s)", prev, curr, f, wait)
			}
		}
	}
}

// TestSlidingWindowEvictStale: keys >= 2 windows old are deleted; a fresh or
// one-window-old key survives.
func TestSlidingWindowEvictStale(t *testing.T) {
	t.Parallel()

	b := &SlidingWindowStrategy{Max: 10, Size: time.Hour}
	require.True(t, b.Take("stale"))
	require.True(t, b.Take("fresh"))

	// evictStale re-reads the clock internally; if the top of the hour fell between
	// computing cur and that read, deleteBefore would advance to cur and wrongly evict
	// "fresh" (window cur-1). Bracket the call with epoch reads and re-stage on a
	// crossing — a retry starts just past the boundary, so it cannot cross again.
	for attempt := 0; ; attempt++ {
		cur := time.Now().UnixNano() / b.size()
		b.mu.Lock()
		b.storage["stale"] = &slidingItem{window: 0, curr: 1}       // epoch: many windows old
		b.storage["fresh"] = &slidingItem{window: cur - 1, curr: 1} // exactly one window old: still has a live prev
		b.mu.Unlock()

		b.evictStale()

		if time.Now().UnixNano()/b.size() == cur {
			break
		}
		require.Less(t, attempt, 3, "the hour boundary kept crossing the evictStale bracket")
	}

	b.mu.RLock()
	_, staleExists := b.storage["stale"]
	_, freshExists := b.storage["fresh"]
	b.mu.RUnlock()
	assert.False(t, staleExists, ">= 2 windows old is evicted")
	assert.True(t, freshExists, "one window old survives (its prev is still live)")
}

// TestSlidingWindowSuppressesBoundaryBurst proves, through the public Take and the
// REAL d==1 roll inside it, that the previous window's spent budget suppresses the
// up-to-2x burst a fixed window admits at its boundary. The rolled state is seeded
// directly (a window that already spent Max, one roll away) instead of sleeping a
// real 100ms window across a boundary, which raced the wall clock: with
// Size=time.Hour, the admit condition Max*(1-e)+curr+1 <= Max caps curr at
// Max*e-1 < Max-1 for any elapsed fraction e<1, hence burst < Max no matter where in
// the hour the test runs. The exact smoothing math stays pinned by the deterministic
// tests above.
func TestSlidingWindowSuppressesBoundaryBurst(t *testing.T) {
	t.Parallel()

	const max = 10
	b := &SlidingWindowStrategy{Max: max, Size: time.Hour}

	for attempt := 0; ; attempt++ {
		cur := time.Now().UnixNano() / b.size()
		b.mu.Lock()
		if b.storage == nil {
			b.storage = make(map[string]*slidingItem)
		}
		// The previous window spent exactly Max; the first Take below performs the
		// genuine curr->prev shift on roll's d==1 path.
		b.storage["k"] = &slidingItem{window: cur - 1, curr: max}
		b.mu.Unlock()

		burst := 0
		for range 100 {
			if b.Take("k") {
				burst++
			}
		}

		// Retry only if the top of the hour fell inside the hammer loop (roll would
		// then see d>=2, clear both counters, and spuriously grant a fresh Max). A
		// retry starts just past the boundary, so it cannot cross again.
		if time.Now().UnixNano()/b.size() == cur {
			assert.Less(t, burst, max, "the previous window suppresses the boundary burst (no 2x)")
			// And the d==1 roll genuinely executed inside Take: the seeded curr=Max
			// moved to prev and the item advanced to the current window. A frozen or
			// mis-wired window computation in Take would leave window==cur-1 (and
			// could pass the burst assert above by never granting at all).
			b.mu.RLock()
			it := b.storage["k"]
			b.mu.RUnlock()
			require.NotNil(t, it)
			assert.EqualValues(t, cur, it.window, "Take's clock read advanced the item to the current window")
			assert.EqualValues(t, max, it.prev, "the spent budget shifted curr->prev on the d==1 roll")
			break
		}
		require.Less(t, attempt, 3, "the hour boundary kept crossing the burst loop")
	}
}

// TestSlidingWindowSingleCleanupGoroutine: the cleanup loop is started exactly once,
// not once per Take (the lifecycle regression the design flagged).
func TestSlidingWindowSingleCleanupGoroutine(t *testing.T) {
	// not parallel: NumGoroutine is process-global, and a non-parallel test runs in the
	// serial phase where no t.Parallel sibling executes — so no other test's cleanup
	// loop spawns mid-assertion. LessOrEqual (not Equal) so transient GC/runtime workers
	// can't flake it; only goroutine GROWTH (the once-per-Take regression) fails it.
	base := runtime.NumGoroutine()
	b := &SlidingWindowStrategy{Max: 1 << 30, Size: time.Hour}

	b.Take("k") // first Take arms the (long-sleeping, never-exiting) cleanup loop
	require.Eventually(t, func() bool { return runtime.NumGoroutine() >= base+1 }, time.Second, 5*time.Millisecond,
		"the first Take starts the cleanup loop")
	after := runtime.NumGoroutine()

	for range 5000 {
		b.Take("k")
	}
	assert.LessOrEqual(t, runtime.NumGoroutine(), after, "no additional goroutine per Take")
}
