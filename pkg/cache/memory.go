package cache

import (
	"bytes"
	"sync"
)

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

// Writer returns a buffer-backed writer; Commit stores it (bodies are in RAM
// either way for this backend).
func (s *MemoryStorage) Writer(key string) (EntryWriter, error) {
	return &memWriter{s: s, key: key}, nil
}

// Delete removes the entry under key.
func (s *MemoryStorage) Delete(key string) {
	s.mu.Lock()
	delete(s.m, key)
	s.mu.Unlock()
	s.lru.remove(key)
}

//nolint:govet
type memWriter struct {
	s    *MemoryStorage
	key  string
	buf  bytes.Buffer
	done bool
}

func (w *memWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

func (w *memWriter) Commit(meta Meta) error {
	if w.done {
		return nil
	}
	w.done = true
	body := append([]byte(nil), w.buf.Bytes()...)
	w.s.mu.Lock()
	w.s.m[w.key] = memEntry{meta: meta, body: body}
	w.s.mu.Unlock()
	for _, victim := range w.s.lru.admit(w.key, meta.Size) {
		w.s.mu.Lock()
		delete(w.s.m, victim)
		w.s.mu.Unlock()
	}
	return nil
}

func (w *memWriter) Abort() {
	w.done = true
	w.buf.Reset()
}
