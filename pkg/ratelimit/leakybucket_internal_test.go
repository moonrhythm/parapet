package ratelimit

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLeakyBucketJanitorStopOnlyWhenEmpty pins the janitor's stop state machine
// (same shape as TestSlidingWindowJanitorStopOnlyWhenEmpty): a sweep that leaves
// live keys keeps it running — including a key protected by a queued waiter — the
// sweep that empties the map marks it stopped in the SAME critical section, and the
// next Take arms it again.
func TestLeakyBucketJanitorStopOnlyWhenEmpty(t *testing.T) {
	t.Parallel()

	b := &LeakyBucketStrategy{PerRequest: time.Millisecond, Size: 1}

	longAgo := time.Now().Add(-time.Hour)
	b.mu.Lock()
	b.storage = map[string]*leakyItem{
		"stale":  {Last: longAgo},           // idle, no waiters: evictable
		"queued": {Last: longAgo, Count: 1}, // a queued waiter protects the item
	}
	b.cleanupRunning = true
	b.mu.Unlock()

	require.False(t, b.evictStale(time.Minute), "a queued waiter keeps its key and the janitor alive")
	b.mu.RLock()
	assert.True(t, b.cleanupRunning)
	assert.Len(t, b.storage, 1)
	_, queuedExists := b.storage["queued"]
	b.mu.RUnlock()
	assert.True(t, queuedExists, "Count > 0 is never evicted (Take sleeps unlocked on it)")

	// The waiter drains; the emptying sweep stops the janitor.
	b.mu.Lock()
	b.storage["queued"].Count = 0
	b.mu.Unlock()
	require.True(t, b.evictStale(time.Minute), "the sweep that empties the map reports stop")
	b.mu.RLock()
	assert.False(t, b.cleanupRunning, "stop is marked under the same lock as the sweep")
	assert.Empty(t, b.storage)
	b.mu.RUnlock()

	// Idle -> active: the next Take restarts the janitor.
	require.True(t, b.Take("k"))
	b.mu.RLock()
	assert.True(t, b.cleanupRunning, "the next Take restarts the janitor")
	b.mu.RUnlock()
}

// TestLeakyBucketJanitorExitsWhenIdle drives the REAL cleanupLoop (via the sweep
// cadence seam) end-to-end: traffic stops, the key ages past the sweep interval,
// the emptying sweep makes the goroutine exit — the #243 leak — and the strategy
// is armed again by the next Take.
func TestLeakyBucketJanitorExitsWhenIdle(t *testing.T) {
	// not parallel: NumGoroutine is process-global (same reasoning as
	// TestSlidingWindowSingleCleanupGoroutine).
	base := runtime.NumGoroutine()
	b := &LeakyBucketStrategy{PerRequest: time.Millisecond, Size: 1, sweepEvery: 5 * time.Millisecond}

	require.True(t, b.Take("k"))
	// Assert the invariant, not an instant: a live janitor — or, if this goroutine
	// stalled past a sweep, an already-drained map (the janitor then stopped
	// correctly). Requiring cleanupRunning alone would race the 5ms sweep.
	b.mu.RLock()
	armed := b.cleanupRunning || len(b.storage) == 0
	b.mu.RUnlock()
	require.True(t, armed, "first Take arms the janitor (or it already drained and stopped)")

	// No further traffic: "k" ages past the 5ms sweep interval and the sweep that
	// evicts it must mark the janitor stopped and exit the goroutine.
	require.Eventually(t, func() bool {
		b.mu.RLock()
		defer b.mu.RUnlock()
		return !b.cleanupRunning && len(b.storage) == 0
	}, 2*time.Second, 2*time.Millisecond, "the janitor stops once the map drains")
	// Plain polling, NOT require.Eventually: Eventually evaluates its condition in a
	// goroutine of its own, which would inflate NumGoroutine by one forever.
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && runtime.NumGoroutine() > base; {
		time.Sleep(2 * time.Millisecond)
	}
	assert.LessOrEqual(t, runtime.NumGoroutine(), base, "the janitor goroutine actually exited (not just flagged stopped)")

	require.True(t, b.Take("k2"))
	b.mu.RLock()
	rearmed := b.cleanupRunning || len(b.storage) == 0 // invariant form, as above
	b.mu.RUnlock()
	require.True(t, rearmed, "the next Take restarts the janitor (or it already drained and stopped)")
}
