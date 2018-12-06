package ratelimit

import (
	"sync"
	"time"
)

// Leaky creates new leaky bucket rate limiter
func Leaky(capacity int) *RateLimiter {
	m := &RateLimiter{
		Key: ClientIP,
		Bucket: &LeakyBucket{
			Capacity: capacity,
		},
	}
	return m
}

// LeakyBucket implements Bucket using leaky bucket algorithm
type LeakyBucket struct {
	mu      sync.Mutex
	storage map[string]int

	Capacity int // Queue size
}

// Take takes a token by pushs request to queue
func (b *LeakyBucket) Take(key string) bool {
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

// Put puts token back to bucket by pop from queue
func (b *LeakyBucket) Put(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.storage == nil {
		return
	}

	if b.storage[key] <= 0 {
		return
	}

	b.storage[key]--
}

// After always return 0
func (b *LeakyBucket) After(string) time.Duration {
	return 0
}
