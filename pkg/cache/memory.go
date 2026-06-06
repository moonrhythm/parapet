package cache

import (
	"bytes"
	"net/http"
	"sync"
)

// MemoryStorage is an in-memory cache backend: bodies are held in RAM and lost
// on restart. Total size is bounded by LRU eviction (the cap passed to NewMemory)
// plus the middleware's per-object cap. Safe for concurrent use. During a fill the
// whole body is buffered in RAM (and retained if the response is cached), so peak
// transient memory is up to MaxFileSize per concurrent miss, independent of the
// byte cap.
type MemoryStorage struct {
	m   map[string]memEntry
	lru *lru
	mu  sync.RWMutex
}

type memEntry struct {
	body []byte
	meta Meta
}

// NewMemory creates an in-memory storage bounded to maxSize total body bytes.
func NewMemory(maxSize int64) *MemoryStorage {
	return &MemoryStorage{m: map[string]memEntry{}, lru: newLRU(maxSize)}
}

// Get returns the entry under key, touching its LRU recency on a hit. The Meta is
// deep-copied so a caller (e.g. the InvalidatedAfter hook) can't mutate the live
// stored entry; the body is returned by reference and must not be mutated (see
// Storage). This matches the disk backend, which returns a fresh Meta per call.
func (s *MemoryStorage) Get(key string) (Meta, []byte, bool) {
	s.mu.RLock()
	e, ok := s.m[key]
	s.mu.RUnlock()
	if !ok {
		return Meta{}, nil, false
	}
	s.lru.touch(key)
	return cloneMeta(e.meta), e.body, true
}

// cloneMeta returns m with its Header map and Vary slice deep-copied, so a caller
// can't mutate (or data-race) the live stored entry's metadata. The body is not
// copied (it is large and read-only by contract). The disk backend gets this for
// free by unmarshaling a fresh Meta per call.
func cloneMeta(m Meta) Meta {
	if m.Header != nil {
		h := make(http.Header, len(m.Header))
		for k, vs := range m.Header {
			h[k] = append([]string(nil), vs...)
		}
		m.Header = h
	}
	if m.Vary != nil {
		m.Vary = append([]string(nil), m.Vary...)
	}
	return m
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

// Range snapshots the current (key, Meta) pairs under the read lock, then calls fn
// for each WITHOUT holding the lock — so fn may Delete entries (which takes the
// write lock) without deadlocking. The snapshot deep-copies each Meta (header map
// and Vary slice included), so a fn that mutates the Meta it receives can neither
// corrupt the live cached entry nor data-race concurrent serving of it.
func (s *MemoryStorage) Range(fn func(key string, m Meta) bool) {
	s.mu.RLock()
	type kv struct {
		key string
		m   Meta
	}
	snap := make([]kv, 0, len(s.m))
	for k, e := range s.m {
		snap = append(snap, kv{k, cloneMeta(e.meta)})
	}
	s.mu.RUnlock()
	for _, e := range snap {
		if !fn(e.key, e.m) {
			return
		}
	}
}

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
	body := bytes.Clone(w.buf.Bytes())
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
	if w.done {
		return // matches diskWriter: Abort after Commit/Abort is a no-op
	}
	w.done = true
	w.buf.Reset()
}
