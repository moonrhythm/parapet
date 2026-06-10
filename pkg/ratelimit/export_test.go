package ratelimit

// QueueCountForTest is a white-box probe for the external (ratelimit_test) concurrent
// queue tests: it reports how many Takes are queued for key, so they can synchronize
// on "the queued Take has reached the block" instead of sleeping and hoping the
// scheduler ran the goroutine. Exported test-only identifiers here are visible to the
// sibling ratelimit_test package and stay out of the shipped API.
func (b *ConcurrentQueueStrategy) QueueCountForTest(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t := b.storage[key]; t != nil {
		return t.QueueCount
	}
	return 0
}
