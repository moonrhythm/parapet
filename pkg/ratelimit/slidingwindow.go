package ratelimit

import (
	"sync"
	"time"
)

// SlidingWindow creates new sliding window rate limiter.
func SlidingWindow(rate int, unit time.Duration) *RateLimiter {
	return New(&SlidingWindowStrategy{
		Max:  rate,
		Size: unit,
	})
}

// SlidingWindowPerSecond creates new sliding window rate limiter per second.
func SlidingWindowPerSecond(rate int) *RateLimiter {
	return SlidingWindow(rate, time.Second)
}

// SlidingWindowPerMinute creates new sliding window rate limiter per minute.
func SlidingWindowPerMinute(rate int) *RateLimiter {
	return SlidingWindow(rate, time.Minute)
}

// SlidingWindowPerHour creates new sliding window rate limiter per hour.
func SlidingWindowPerHour(rate int) *RateLimiter {
	return SlidingWindow(rate, time.Hour)
}

// SlidingWindowStrategy implements Strategy using the sliding-window-counter
// algorithm: the effective count is a time-weighted blend of the current
// fixed window's count and the previous window's count, the previous count fading
// out linearly as the current window elapses. This smooths the up-to-2x burst a
// plain fixed window admits across its boundary, while keeping O(1) memory per key
// (two counters) — unlike an exact sliding log, whose per-key memory is
// attacker-controlled (O(admitted requests), a DoS-amplification footgun at the
// edge).
//
// It is an APPROXIMATION, not exact: the blend assumes the previous window's
// requests were uniformly distributed in time. Back-loaded traffic (a burst at the
// tail of a window) can briefly over-admit and front-loaded traffic can briefly
// over-throttle, both bounded and typically under ~1% of Max. Use it when you want
// to remove the fixed window's boundary doubling cheaply; reach for an exact log
// only if a hard per-window guarantee is worth the unbounded per-key memory.
//
// Per-KEY state is O(1), but the working set (live map entries) is O(distinct keys
// seen in the last ~2 windows): there is no max-entries cap, so a unique-key flood
// inflates memory until the background sweep reclaims it — unlike FixedWindow, which
// clears its whole map each window boundary (a counter cannot, since the previous
// window's count is load-bearing for the blend). Like the other limiters, window
// indices ride the wall clock: a non-monotonic step that re-crosses a boundary can
// shift at most one window's budget (bounded by Max, never FixedWindow's full reset).
//
//nolint:govet // fields grouped by role (state, then config) for readability
type SlidingWindowStrategy struct {
	mu             sync.RWMutex
	storage        map[string]*slidingItem
	cleanupRunning bool          // janitor liveness; guarded by mu (see evictStale)
	sweepEvery     time.Duration // test seam: sweep cadence override; <= 0 uses max(2*Size, 1m)

	Max  int           // Max token per window; Max <= 0 admits nothing
	Size time.Duration // Window size (the trailing interval the limit applies over)
}

// slidingItem is the entire per-key state: a fixed 3-int struct regardless of Max
// or traffic. window is the fixed-window index (UnixNano/Size) that curr belongs to.
type slidingItem struct {
	window int64 // window index that curr counts
	curr   int   // count in the current window
	prev   int   // count in the previous window
}

// size returns the window size in nanoseconds, guarding the divide-by-zero that a
// raw int64(b.Size) would hit when misconfigured (Size is a divisor on every path).
// Only reachable via a hand-built struct; the constructors always set Size.
func (b *SlidingWindowStrategy) size() int64 {
	if b.Size <= 0 {
		return int64(time.Second)
	}
	return int64(b.Size)
}

// roll advances the item to currentWindow, shifting curr->prev across exactly one
// boundary and clearing both across a larger (idle) gap. The d <= 0 branch also
// absorbs a backward clock step (no negative shift). Caller holds mu.Lock.
func (t *slidingItem) roll(currentWindow int64) {
	switch d := currentWindow - t.window; {
	case d <= 0:
		// same window, or clock went backward: nothing to roll
	case d == 1:
		t.prev, t.curr = t.curr, 0
	default:
		t.prev, t.curr = 0, 0
	}
	t.window = currentWindow
}

// weightedCount returns the time-weighted effective count at now (ns since epoch):
// the previous window's count linearly faded out as we advance through the current
// window. At a boundary (elapsed -> 0) the full prev count is included; by the end
// (elapsed -> 1) it has fully decayed. size must be > 0.
func weightedCount(prev, curr int, now, size int64) float64 {
	elapsed := float64(now%size) / float64(size) // fraction [0,1) of current window gone
	return float64(prev)*(1-elapsed) + float64(curr)
}

// Take admits a request iff the weighted trailing-window count stays within Max, so
// a steady stream is capped at APPROXIMATELY Max over any trailing window (exactly
// Max for uniform/front-loaded traffic; see the type doc for the boundary
// approximation). Max <= 0 admits nothing.
func (b *SlidingWindowStrategy) Take(key string) bool {
	size := b.size()
	now := time.Now().UnixNano()
	currentWindow := now / size

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.storage == nil {
		b.storage = make(map[string]*slidingItem)
	}
	t := b.storage[key]
	if t == nil {
		t = &slidingItem{window: currentWindow}
		b.storage[key] = t
	}
	t.roll(currentWindow)

	// The map is non-empty from here on, so a janitor must be live. mu is the same
	// lock the janitor stops itself under (evictStale), so the two transitions
	// serialize: either this Take sees the stop and restarts the loop, or its key
	// was inserted before the final sweep and kept the janitor alive.
	if !b.cleanupRunning {
		b.cleanupRunning = true
		go b.cleanupLoop()
	}

	// Count the request under decision against the limit so the cap holds over the
	// trailing window, not just within the fixed window.
	if weightedCount(t.prev, t.curr, now, size)+1 > float64(b.Max) {
		return false
	}
	t.curr++
	return true
}

// Put does nothing — this is an arrival-rate limiter, not a concurrency limiter.
func (b *SlidingWindowStrategy) Put(string) {}

// After returns how long until the next request for key would be admitted. It never
// mutates shared state: it reads the item under RLock and computes on locals.
//
// It may return 0 even immediately after Take returned false for the same key, if a
// window boundary falls between the two calls (the read-only roll then shifts
// curr->prev, freeing budget) — the client genuinely can take now, so do not treat
// "After > 0 while blocked" as an invariant.
func (b *SlidingWindowStrategy) After(key string) time.Duration {
	size := b.size()
	now := time.Now().UnixNano()
	currentWindow := now / size

	b.mu.RLock()
	defer b.mu.RUnlock()

	t := b.storage[key]
	if t == nil {
		return 0
	}

	// Read-only roll: compute as if rolled, without mutating t.
	var prev, curr int
	switch d := currentWindow - t.window; {
	case d <= 0:
		prev, curr = t.prev, t.curr
	case d == 1:
		prev, curr = t.curr, 0
	default:
		prev, curr = 0, 0
	}
	return afterAt(b.Max, prev, curr, size, now)
}

// afterAt computes the wait until the next admit for an item already rolled to
// (prev, curr) at time now (ns), with window size size (> 0). It is pure — no shared
// state, no clock — so the closed-form decay is testable with an injected now. It is
// never too optimistic: at now+afterAt the request is admissible, so a client that
// honors the Retry-After does not retry into another denial.
func afterAt(maxTokens, prev, curr int, size, now int64) time.Duration {
	if weightedCount(prev, curr, now, size)+1 <= float64(maxTokens) {
		return 0 // can take now
	}

	curFrac := float64(now%size) / float64(size)
	toBoundary := time.Duration((now/size+1)*size - now)

	// Relief within THIS window: the previous window's count decays. Possible only
	// while curr alone is under the limit (else no amount of prev-decay admits) and
	// prev is actually decaying. Solve prev*(1-frac) + curr + 1 == maxTokens.
	if prev > 0 && curr+1 <= maxTokens {
		targetFrac := 1 - (float64(maxTokens)-float64(curr)-1)/float64(prev)
		// targetFrac < 1 guarantees the wait lands strictly before the boundary: its
		// margin from 1 is >= size/prev, which dominates the +1ns, so no clamp to
		// toBoundary is needed (the +1ns can never tip past the window's end).
		if targetFrac > curFrac && targetFrac < 1 {
			return time.Duration((targetFrac-curFrac)*float64(size)) + 1 // +1ns: never report 0 while blocked
		}
	}

	// Relief is at/after the next boundary, where curr becomes the new prev (the old
	// prev is dropped). If curr fits under the limit it admits right at the boundary.
	if curr+1 <= maxTokens {
		return toBoundary
	}
	// A zero/negative limit never admits (curr stays 0), so the boundary is the bounded
	// advisory value — no decay solve applies (and it avoids a nextFrac > 1 below).
	if maxTokens <= 0 {
		return toBoundary
	}
	// curr is at/over the limit (a full window): even at the boundary the new prev
	// still blocks, so wait for IT to decay into the next window too. Solve
	// curr*(1-frac) + 1 == maxTokens. curr > 0 here (curr+1 > maxTokens >= 1).
	nextFrac := 1 - (float64(maxTokens)-1)/float64(curr)
	return toBoundary + time.Duration(nextFrac*float64(size)) + 1
}

// cleanupLoop evicts keys idle for >= 2 windows (their curr == prev == 0 after roll,
// so they contribute nothing). It runs as its own goroutine, started by Take, and
// EXITS when a sweep leaves the map empty rather than living for the process
// lifetime: a discarded strategy (hot-reloaded config, per-tenant limiters built and
// dropped at runtime) drains within ~2 sweeps and becomes fully collectable instead
// of leaking a goroutine. The next Take restarts it on the idle->active transition —
// same shape as LeakyBucketStrategy.cleanupLoop.
func (b *SlidingWindowStrategy) cleanupLoop() {
	every := b.sweepEvery
	if every <= 0 {
		every = 2 * time.Duration(b.size())
		if every < time.Minute {
			every = time.Minute
		}
	}

	for {
		time.Sleep(every)
		if b.evictStale() {
			return
		}
	}
}

// evictStale deletes keys >= 2 windows old (window < currentWindow-1): roll has
// already zeroed both their counts, so deleting them loses nothing. A key one window
// old still has a live prev and survives, deleted on a later sweep.
//
// It reports whether the sweep left the map empty, having then ALSO marked the
// janitor stopped — both inside the one mu critical section, so a concurrent Take
// cannot observe a non-empty map with no janitor: it either sees cleanupRunning ==
// false and restarts the loop, or its key landed before the sweep and kept the map
// non-empty.
func (b *SlidingWindowStrategy) evictStale() (stopped bool) {
	deleteBefore := time.Now().UnixNano()/b.size() - 1

	b.mu.Lock()
	defer b.mu.Unlock()

	for k, t := range b.storage {
		if t.window < deleteBefore {
			delete(b.storage, k)
		}
	}
	if len(b.storage) > 0 {
		return false
	}
	b.cleanupRunning = false
	return true
}
