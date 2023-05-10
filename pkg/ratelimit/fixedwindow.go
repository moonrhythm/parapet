package ratelimit

import (
	"sync"
	"time"
)

// FixedWindow creates new fixed window rate limiter
func FixedWindow(rate int, unit time.Duration) *RateLimiter {
	return New(&FixedWindowStrategy{
		Max:  rate,
		Size: unit,
	})
}

// FixedWindowPerSecond creates new rate limiter per second
func FixedWindowPerSecond(rate int) *RateLimiter {
	return FixedWindow(rate, time.Second)
}

// FixedWindowPerMinute creates new rate limiter per minute
func FixedWindowPerMinute(rate int) *RateLimiter {
	return FixedWindow(rate, time.Minute)
}

// FixedWindowPerHour creates new rate limiter per hour
func FixedWindowPerHour(rate int) *RateLimiter {
	return FixedWindow(rate, time.Hour)
}

// FixedWindowStrategy implements Strategy using fixed window algorithm
//
//nolint:govet
type FixedWindowStrategy struct {
	mu         sync.RWMutex
	lastWindow int64
	storage    map[string]int

	Max  int           // Max token per window
	Size time.Duration // Window size
}

// Take takes a token from bucket, return true if token available to take
func (b *FixedWindowStrategy) Take(key string) bool {
	currentWindow := time.Now().UnixNano() / int64(b.Size)

	b.mu.Lock()
	defer b.mu.Unlock()

	// is window outdated ?
	if b.lastWindow != currentWindow {
		// window outdated, create new window
		b.lastWindow = currentWindow
		if len(b.storage) > 0 || b.storage == nil {
			b.storage = make(map[string]int)
		}
	}

	// get available token
	available, ok := b.storage[key]
	if !ok {
		// bucket of given key not exists
		// set available to max
		available = b.Max
	}

	// can we take a token ?
	if available <= 0 {
		// token not available
		return false
	}

	// take a token
	b.storage[key] = available - 1

	return true
}

// Put does nothing
func (b *FixedWindowStrategy) Put(string) {}

// After returns next time that can take again
func (b *FixedWindowStrategy) After(key string) time.Duration {
	now := time.Now()
	currentWindow := now.UnixNano() / int64(b.Size)

	b.mu.RLock()
	defer b.mu.RUnlock()

	// is current window outdated ?
	if b.lastWindow != currentWindow {
		// window outdated, can take now
		return 0
	}

	if b.storage == nil {
		// no storage exists, can take now
		return 0
	}

	// get available token
	available, ok := b.storage[key]
	if !ok {
		// bucket of given key not exists
		// can take now
		return 0
	}

	if available > 0 {
		// still more tokens in bucket
		// can take now
		return 0
	}

	// no more token left
	nextWindow := now.Truncate(b.Size).Add(b.Size)
	return nextWindow.Sub(now)
}
