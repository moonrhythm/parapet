package cache

import "sync"

// MemoryStorage is an in-memory cache backend: bodies are held in RAM and lost
// on restart. Total size is bounded by LRU eviction (the cap passed to NewMemory)
// plus the middleware's per-object cap. Safe for concurrent use.
//
//nolint:govet
type MemoryStorage struct {
	mu  sync.RWMutex
	m   map[string]memEntry
	lru *lru
}

//nolint:govet
type memEntry struct {
	meta Meta
	body []byte
}

// NewMemory creates an in-memory storage bounded to maxSize total body bytes.
func NewMemory(maxSize int64) *MemoryStorage {
	return &MemoryStorage{m: map[string]memEntry{}, lru: newLRU(maxSize)}
}

// Get returns the entry under key, touching its LRU recency on a hit.
func (s *MemoryStorage) Get(key string) (Meta, []byte, bool) {
	s.mu.RLock()
	e, ok := s.m[key]
	s.mu.RUnlock()
	if !ok {
		return Meta{}, nil, false
	}
	s.lru.touch(key)
	return e.meta, e.body, true
}

// Set stores body+meta under key and evicts least-recently-used entries to stay
// within the byte cap. The middleware passes a body it no longer mutates.
func (s *MemoryStorage) Set(key string, meta Meta, body []byte) {
	s.mu.Lock()
	s.m[key] = memEntry{meta: meta, body: body}
	s.mu.Unlock()
	for _, victim := range s.lru.admit(key, meta.Size) {
		s.mu.Lock()
		delete(s.m, victim)
		s.mu.Unlock()
	}
}

// Delete removes the entry under key.
func (s *MemoryStorage) Delete(key string) {
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
	s.lru.remove(key)
}
