package ratelimit

import (
	"sync"
	"time"
)

// Concurrent creates new concurrent rate limiter
func Concurrent(capacity int) *RateLimiter {
	m := &RateLimiter{
		Key: ClientIP,
		Bucket: &ConcurrentBucket{
			Capacity: capacity,
		},
	}
	return m
}

// ConcurrentBucket implements Bucket
// that allow only max concurrent requests at a time
// other requests will drop
type ConcurrentBucket struct {
	mu      sync.Mutex
	storage map[string]int

	Capacity int // Max concurrent at a time
}

// Take takes a token
func (b *ConcurrentBucket) Take(key string) bool {
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

// Put puts token back to bucket
func (b *ConcurrentBucket) Put(key string) {
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

// After always return 0
func (b *ConcurrentBucket) After(string) time.Duration {
	return 0
}
