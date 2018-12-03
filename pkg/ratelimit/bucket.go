package ratelimit

import (
	"sync"
)

type bucket struct {
	mu sync.Mutex
	t  int64
	d  map[string]int
}

func (b *bucket) Incr(t int64, k string, max int) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.t != t {
		b.t = t
		if len(b.d) > 0 || b.d == nil {
			b.d = make(map[string]int)
		}
	}

	x := b.d[k] + 1
	if x <= max {
		b.d[k] = x
	}
	return x
}
