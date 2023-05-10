package ratelimit

import (
	"sync"
	"time"
)

// ConcurrentQueue creates new concurrent queue rate limiter
func ConcurrentQueue(capacity, size int) *RateLimiter {
	return New(&ConcurrentQueueStrategy{
		Capacity: capacity,
		Size:     size,
	})
}

// ConcurrentQueueStrategy implements Strategy
// that allow only max concurrent requests at a time
// other requests will queue, until queue full then the request wil drop
//
//nolint:govet
type ConcurrentQueueStrategy struct {
	mu      sync.Mutex
	storage map[string]*concurrentQueueItem

	Capacity int // max concurrent at a time
	Size     int // queue size
}

type concurrentQueueItem struct {
	Process      chan struct{}
	ProcessCount int
	QueueCount   int
}

// Take returns true if current requests less than capacity
func (b *ConcurrentQueueStrategy) Take(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.storage == nil {
		b.storage = make(map[string]*concurrentQueueItem)
	}

	if b.storage[key] == nil {
		b.storage[key] = &concurrentQueueItem{
			Process: make(chan struct{}, b.Capacity),
		}
	}

	t := b.storage[key]

	if t.QueueCount >= b.Size {
		// queue full, drop the request
		return false
	}
	t.ProcessCount++
	t.QueueCount++
	b.mu.Unlock()

	t.Process <- struct{}{}

	b.mu.Lock()
	t.QueueCount--
	return true
}

// Put removes requests count
func (b *ConcurrentQueueStrategy) Put(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.storage == nil {
		return
	}

	t := b.storage[key]
	if t == nil {
		return
	}

	<-t.Process
	t.ProcessCount--

	if t.ProcessCount <= 0 && t.QueueCount <= 0 {
		delete(b.storage, key)
	}
}

// After always return 0, since we don't know when request will finish
func (b *ConcurrentQueueStrategy) After(string) time.Duration {
	return 0
}
