package ratelimit

import (
	"sync"
	"time"
)

// Leaky creates new leaky bucket rate limiter
func Leaky(perRequest time.Duration, capacity int) *RateLimiter {
	return New(&LeakyBucketStrategy{
		PerRequest: perRequest,
		Capacity:   capacity,
	})
}

// LeakyBucketStrategy implements Strategy using leaky bucket algorithm
type LeakyBucketStrategy struct {
	mu      sync.RWMutex
	storage map[string]*leakyItem
	once    sync.Once

	PerRequest time.Duration // time per request
	Capacity   int           // queue size
}

type leakyItem struct {
	Last  time.Time // last request time
	Count int       // requests in queue
}

// Take waits until token can be take, unless queue full will return false
func (b *LeakyBucketStrategy) Take(key string) bool {
	b.once.Do(b.cleanupLoop)

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.storage == nil {
		b.storage = make(map[string]*leakyItem)
	}

	if b.storage[key] == nil {
		b.storage[key] = new(leakyItem)
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

	if t.Count >= b.Capacity {
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

func (b *LeakyBucketStrategy) cleanupLoop() {
	maxDuration := b.PerRequest + time.Second
	if maxDuration < time.Minute {
		maxDuration = time.Minute
	}

	cleanup := func() {
		deleteBefore := time.Now().Add(-maxDuration)

		b.mu.Lock()
		defer b.mu.Unlock()

		for k, t := range b.storage {
			if t.Count <= 0 && t.Last.Before(deleteBefore) {
				delete(b.storage, k)
			}
		}
	}

	go func() {
		for {
			time.Sleep(maxDuration)
			cleanup()
		}
	}()
}
