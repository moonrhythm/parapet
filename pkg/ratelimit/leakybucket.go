package ratelimit

import (
	"sync"
	"time"
)

// LeakyBucket creates new leaky bucket rate limiter
func LeakyBucket(perRequest time.Duration, size int) *RateLimiter {
	return New(&LeakyBucketStrategy{
		PerRequest: perRequest,
		Size:       size,
	})
}

// LeakyBucketStrategy implements Strategy using leaky bucket algorithm
//
//nolint:govet
type LeakyBucketStrategy struct {
	mu             sync.RWMutex
	storage        map[string]*leakyItem
	cleanupRunning bool          // janitor liveness; guarded by mu (see evictStale)
	sweepEvery     time.Duration // test seam: sweep cadence + idle age override; <= 0 uses max(PerRequest+1s, 1m)

	PerRequest time.Duration // time per request
	Size       int           // queue size
}

type leakyItem struct {
	Last  time.Time // last request time
	Count int       // requests in queue
}

// Take waits until token can be take, unless queue full will return false
func (b *LeakyBucketStrategy) Take(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.storage == nil {
		b.storage = make(map[string]*leakyItem)
	}

	if b.storage[key] == nil {
		b.storage[key] = new(leakyItem)
	}

	// The map is non-empty from here on, so a janitor must be live. mu is the same
	// lock the janitor stops itself under (evictStale), so the two transitions
	// serialize: either this Take sees the stop and restarts the loop, or its key
	// was inserted before the final sweep and kept the janitor alive.
	if !b.cleanupRunning {
		b.cleanupRunning = true
		go b.cleanupLoop()
	}

	t := b.storage[key]

	now := time.Now()

	// first request ?
	if t.Last.IsZero() {
		t.Last = now
		return true
	}

	next := t.Last.Add(b.PerRequest)
	sleep := next.Sub(now)
	if sleep <= 0 {
		t.Last = now
		return true
	}

	if t.Count >= b.Size {
		// queue full, drop the request
		return false
	}

	t.Last = next

	t.Count++
	b.mu.Unlock()

	time.Sleep(sleep)

	b.mu.Lock()
	t.Count--

	return true
}

// Put do nothing
func (b *LeakyBucketStrategy) Put(string) {}

// After returns time that can take again
func (b *LeakyBucketStrategy) After(key string) time.Duration {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.storage == nil {
		return 0
	}

	t := b.storage[key]
	if t == nil {
		return 0
	}

	now := time.Now()
	next := t.Last.Add(b.PerRequest)
	if next.Before(now) {
		return 0
	}

	return next.Sub(now)
}

// cleanupLoop evicts keys idle for >= one sweep interval. It runs as its own
// goroutine, started by Take, and EXITS when a sweep leaves the map empty rather
// than living for the process lifetime: a discarded strategy drains and becomes
// fully collectable instead of leaking a goroutine. The next Take restarts it on
// the idle->active transition — same shape as SlidingWindowStrategy.cleanupLoop.
func (b *LeakyBucketStrategy) cleanupLoop() {
	every := b.sweepEvery
	if every <= 0 {
		every = b.PerRequest + time.Second
		if every < time.Minute {
			every = time.Minute
		}
	}

	for {
		time.Sleep(every)
		if b.evictStale(every) {
			return
		}
	}
}

// evictStale deletes keys with no queued waiters whose last admit is older than
// maxAge. Items with queued waiters (Count > 0) are never evicted, so the unlocked
// sleep inside Take cannot lose its item out from under it.
//
// It reports whether the sweep left the map empty, having then ALSO marked the
// janitor stopped — both inside the one mu critical section, so a concurrent Take
// cannot observe a non-empty map with no janitor: it either sees cleanupRunning ==
// false and restarts the loop, or its key landed before the sweep and kept the map
// non-empty.
func (b *LeakyBucketStrategy) evictStale(maxAge time.Duration) (stopped bool) {
	deleteBefore := time.Now().Add(-maxAge)

	b.mu.Lock()
	defer b.mu.Unlock()

	for k, t := range b.storage {
		if t.Count <= 0 && t.Last.Before(deleteBefore) {
			delete(b.storage, k)
		}
	}
	if len(b.storage) > 0 {
		return false
	}
	b.cleanupRunning = false
	return true
}
