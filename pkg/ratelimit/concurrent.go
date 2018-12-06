package ratelimit

import (
	"sync"
	"time"
)

// Concurrent creates new concurrent rate limiter
func Concurrent(capacity int) *RateLimiter {
	return New(&ConcurrentStrategy{
		Capacity: capacity,
	})
}

// ConcurrentStrategy implements Strategy
// that allow only max concurrent requests at a time
// other requests will drop
type ConcurrentStrategy struct {
	mu      sync.Mutex
	storage map[string]int

	Capacity int // Max concurrent at a time
}

// Take returns true if current requests less than capacity
func (b *ConcurrentStrategy) Take(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.storage == nil {
		b.storage = make(map[string]int)
	}

	if b.storage[key] >= b.Capacity {
		return false
	}

	b.storage[key]++

	return true
}

// Put removes requests count
func (b *ConcurrentStrategy) Put(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.storage == nil {
		return
	}

	if b.storage[key] <= 0 {
		return
	}

	b.storage[key]--

	if b.storage[key] <= 0 {
		delete(b.storage, key)
	}
}

// After always return 0, since we don't know when request will finish
func (b *ConcurrentStrategy) After(string) time.Duration {
	return 0
}
