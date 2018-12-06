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
type ConcurrentQueueStrategy struct {
	mu      sync.Mutex
	storage map[string]*concurrentQueueItem

	Capacity int // max concurrent at a time
	Size     int // queue size
}

type concurrentQueueItem struct {
	Ch    chan struct{}
	Count int // requests in queue
}

// Take returns true if current requests less than capacity
func (b *ConcurrentQueueStrategy) Take(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.storage == nil {
		b.storage = make(map[string]*concurrentQueueItem)
	}

	if b.storage[key] == nil {
		b.storage[key] = new(concurrentQueueItem)
	}

	t := b.storage[key]
	if t.Ch == nil {
		t.Ch = make(chan struct{}, b.Capacity)
	}

	if t.Count >= b.Size {
		// queue full, drop the request
		return false
	}

	t.Count++
	b.mu.Unlock()

	t.Ch <- struct{}{}

	b.mu.Lock()
	t.Count--

	return true
}

// Put removes requests count
func (b *ConcurrentQueueStrategy) Put(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.storage == nil {
		return
	}

	if b.storage[key] == nil {
		return
	}

	<-b.storage[key].Ch

	if b.storage[key].Count <= 0 {
		close(b.storage[key].Ch)
		delete(b.storage, key)
	}
}

// After always return 0, since we don't know when request will finish
func (b *ConcurrentQueueStrategy) After(string) time.Duration {
	return 0
}
