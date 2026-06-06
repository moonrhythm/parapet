package cache

import (
	"container/list"
	"sync"
)

// lru bounds a backend's total stored bytes by least-recently-used eviction. It
// tracks keys and their byte weights; the owning Storage holds the actual data
// and deletes a key's data when lru returns it as a victim. Both the memory and
// disk backends embed an lru.
type lru struct {
	ll    *list.List               // front = most recently used
	items map[string]*list.Element // key -> element holding *lruItem
	max   int64
	cur   int64
	mu    sync.Mutex
}

type lruItem struct {
	key  string
	size int64
}

func newLRU(max int64) *lru {
	return &lru{max: max, ll: list.New(), items: map[string]*list.Element{}}
}

// admit inserts or updates key with size and returns the keys evicted to stay
// within the cap (most-recently-used kept). The caller deletes the evicted
// entries' data.
func (l *lru) admit(key string, size int64) []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if el, ok := l.items[key]; ok {
		it := el.Value.(*lruItem)
		l.cur += size - it.size
		it.size = size
		l.ll.MoveToFront(el)
	} else {
		l.items[key] = l.ll.PushFront(&lruItem{key: key, size: size})
		l.cur += size
	}

	var evicted []string
	for l.cur > l.max {
		back := l.ll.Back()
		if back == nil {
			break
		}
		it := back.Value.(*lruItem)
		if it.key == key {
			// Never evict the entry we just admitted to satisfy its own admission
			// (a single object larger than the cap); the per-object cap prevents
			// this in practice. Stop to avoid an infinite loop.
			break
		}
		l.ll.Remove(back)
		delete(l.items, it.key)
		l.cur -= it.size
		evicted = append(evicted, it.key)
	}
	return evicted
}

// touch marks key as most-recently-used (called on a cache hit). No-op if absent.
func (l *lru) touch(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if el, ok := l.items[key]; ok {
		l.ll.MoveToFront(el)
	}
}

// remove drops key from the accounting (called when its data is deleted). No-op
// if absent.
func (l *lru) remove(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if el, ok := l.items[key]; ok {
		it := el.Value.(*lruItem)
		l.ll.Remove(el)
		delete(l.items, key)
		l.cur -= it.size
	}
}

// size reports the current tracked total (for tests/observability).
func (l *lru) size() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cur
}
