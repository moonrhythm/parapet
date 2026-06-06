package cache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// reapMinAge gates orphan/torn-write reaping during the startup scan: a file is
// only deleted if its mtime is at least this old, so a concurrent in-flight
// commit (body renamed in, .meta not yet) is never touched.
const reapMinAge = 60 * time.Second

// minKeyLen is the shortest key the disk layout supports (the shard dir is the
// first 2 chars). The middleware always passes 32-hex keys; this guards a direct
// Storage caller from panicking shardDir.
const minKeyLen = 2

// DiskStorage is a disk-backed cache backend. Entries survive restarts and the
// body is streamed to disk, so the cache isn't bounded by RSS. The total on-disk
// byte cap is held by an in-memory LRU, re-seeded from disk by a background
// startup scan that also reaps orphans, torn writes, and expired entries. Safe
// for concurrent use by a single Cache (see Storage).
//
// Layout (sharded by the first 2 hex chars of the key):
//
//	<dir>/<aa>/<key>.body   response body bytes
//	<dir>/<aa>/<key>.meta   JSON sidecar (written last)
//	<dir>/tmp/<key>.<seq>   in-progress writes, atomically renamed on commit
//
//nolint:govet
type DiskStorage struct {
	dir string
	seq atomic.Uint64
	lru *lru
}

// NewDisk creates (or opens) a disk storage rooted at dir, bounded to maxSize
// total body bytes. It starts a background scan that re-seeds the byte cap from
// surviving entries and reaps orphans/expired files off the serving path — the
// cap simply lags until the scan completes. Returns an error only if the dir
// can't be initialized.
func NewDisk(dir string, maxSize int64) (*DiskStorage, error) {
	if dir == "" {
		return nil, errors.New("cache: empty dir")
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o755); err != nil {
		return nil, err
	}
	s := &DiskStorage{dir: dir, lru: newLRU(maxSize)}
	go s.scan(time.Now())
	return s, nil
}

func (s *DiskStorage) shardDir(key string) string { return filepath.Join(s.dir, key[:2]) }
func (s *DiskStorage) bodyPath(key string) string {
	return filepath.Join(s.shardDir(key), key+".body")
}
func (s *DiskStorage) metaPath(key string) string {
	return filepath.Join(s.shardDir(key), key+".meta")
}
func (s *DiskStorage) tempPath(prefix string) string {
	return filepath.Join(s.dir, "tmp", prefix+"."+strconv.FormatUint(s.seq.Add(1), 10))
}

// Get reads the entry under key, touching its LRU recency on a hit. ok=false on
// any miss / corruption / torn read (fail-static — treated as a cache miss). The
// body length is checked against meta.Size so a reader can never serve an old
// meta paired with a concurrently-rewritten body (the two files are read
// non-atomically; this guards the framing-corruption case).
func (s *DiskStorage) Get(key string) (Meta, []byte, bool) {
	if len(key) < minKeyLen {
		return Meta{}, nil, false
	}
	mb, err := os.ReadFile(s.metaPath(key))
	if err != nil {
		return Meta{}, nil, false // clean miss or unreadable
	}
	var m Meta
	if err := json.Unmarshal(mb, &m); err != nil {
		return Meta{}, nil, false // corrupt sidecar
	}
	body, err := os.ReadFile(s.bodyPath(key))
	if err != nil || int64(len(body)) != m.Size {
		return Meta{}, nil, false // meta without body (torn), or meta/body size disagree
	}
	s.lru.touch(key)
	return m, body, true
}

// Writer streams a new entry's body to a temp file; Commit fsyncs + renames it
// into place (body first, meta last) and admits it to the byte cap; Abort
// discards the temp file. Returns an error if the temp file can't be created.
func (s *DiskStorage) Writer(key string) (EntryWriter, error) {
	if len(key) < minKeyLen {
		return nil, errors.New("cache: key too short")
	}
	tmp := s.tempPath(key)
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	return &diskWriter{s: s, key: key, f: f, tmp: tmp}, nil
}

// Delete removes the entry under key (meta first, so a half-deleted entry reads
// as a clean miss rather than a torn read) and its LRU accounting.
func (s *DiskStorage) Delete(key string) {
	if len(key) < minKeyLen {
		return
	}
	s.removeFiles(key)
	s.lru.remove(key)
}

func (s *DiskStorage) removeFiles(key string) {
	if len(key) < minKeyLen {
		return // guard shardDir(key[:2]) against a malformed key
	}
	os.Remove(s.metaPath(key))
	os.Remove(s.bodyPath(key))
}

// Range walks the shard dirs reading each entry's .meta sidecar and calls fn(key,
// Meta), stopping early if fn returns false. It holds no lock, so fn may Delete the
// entry it is visiting (the keys come from a directory snapshot taken before fn
// runs). Unreadable/corrupt sidecars are skipped. For maintenance only, off the
// serving path.
func (s *DiskStorage) Range(fn func(key string, m Meta) bool) {
	shards, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, sh := range shards {
		if !sh.IsDir() || sh.Name() == "tmp" {
			continue
		}
		shardPath := filepath.Join(s.dir, sh.Name())
		ents, err := os.ReadDir(shardPath)
		if err != nil {
			continue
		}
		for _, e := range ents {
			name := e.Name()
			if !strings.HasSuffix(name, ".meta") {
				continue
			}
			key := strings.TrimSuffix(name, ".meta")
			mb, err := os.ReadFile(filepath.Join(shardPath, name))
			if err != nil {
				continue
			}
			var m Meta
			if err := json.Unmarshal(mb, &m); err != nil {
				continue
			}
			if !fn(key, m) {
				return
			}
		}
	}
}

//nolint:govet
type diskWriter struct {
	s    *DiskStorage
	key  string
	f    *os.File
	tmp  string
	done bool
}

func (w *diskWriter) Write(p []byte) (int, error) { return w.f.Write(p) }

// Commit fsyncs the temp body, atomically renames it to <key>.body, then writes
// <key>.meta LAST (its presence implies a complete body), and admits to the byte
// cap. NOTE: the fsync runs synchronously on the caller's goroutine — the
// middleware calls Commit on the request goroutine (after the client already has
// the full response) and before it releases the fill lock, so a slow fsync holds
// the connection and briefly blocks waiting followers.
func (w *diskWriter) Commit(meta Meta) error {
	if w.done {
		return nil
	}
	w.done = true
	s := w.s
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		os.Remove(w.tmp)
		return err
	}
	if err := w.f.Close(); err != nil {
		os.Remove(w.tmp)
		return err
	}
	if err := os.MkdirAll(s.shardDir(w.key), 0o755); err != nil {
		os.Remove(w.tmp)
		return err
	}
	bodyPath := s.bodyPath(w.key)
	if err := os.Rename(w.tmp, bodyPath); err != nil {
		os.Remove(w.tmp)
		return err
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		os.Remove(bodyPath)
		return err
	}
	if err := atomicWriteRename(s.metaPath(w.key), mb, s.tempPath(w.key+".meta")); err != nil {
		os.Remove(bodyPath) // roll back so a body without meta isn't left
		return err
	}
	for _, victim := range s.lru.admit(w.key, meta.Size) {
		s.removeFiles(victim)
	}
	return nil
}

func (w *diskWriter) Abort() {
	if w.done {
		return
	}
	w.done = true
	w.f.Close()
	os.Remove(w.tmp)
}

// scan walks the cache dir, reaping orphans / torn writes / expired entries
// (age-gated so in-flight commits are spared) and re-admitting the survivors to
// the LRU so the byte cap holds across restarts.
func (s *DiskStorage) scan(now time.Time) {
	// Reap stale temp files (abandoned in-progress writes).
	tmpDir := filepath.Join(s.dir, "tmp")
	if ents, err := os.ReadDir(tmpDir); err == nil {
		for _, e := range ents {
			reapIfStale(filepath.Join(tmpDir, e.Name()), now)
		}
	}

	shards, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, sh := range shards {
		if !sh.IsDir() || sh.Name() == "tmp" {
			continue
		}
		shardPath := filepath.Join(s.dir, sh.Name())
		ents, err := os.ReadDir(shardPath)
		if err != nil {
			continue
		}
		bodies := map[string]struct{}{}
		var metas []string
		for _, e := range ents {
			name := e.Name()
			switch {
			case strings.HasSuffix(name, ".body"):
				bodies[strings.TrimSuffix(name, ".body")] = struct{}{}
			case strings.HasSuffix(name, ".meta"):
				metas = append(metas, strings.TrimSuffix(name, ".meta"))
			}
		}
		seenMeta := map[string]struct{}{}
		for _, key := range metas {
			seenMeta[key] = struct{}{}
			mp := filepath.Join(shardPath, key+".meta")
			mb, err := os.ReadFile(mp)
			if err != nil {
				continue
			}
			var m Meta
			if err := json.Unmarshal(mb, &m); err != nil {
				reapIfStale(mp, now) // corrupt sidecar
				reapIfStale(filepath.Join(shardPath, key+".body"), now)
				continue
			}
			if _, hasBody := bodies[key]; !hasBody {
				reapIfStale(mp, now) // meta without body (torn)
				continue
			}
			if now.After(time.Unix(0, m.FreshUntil)) {
				os.Remove(mp) // expired
				os.Remove(filepath.Join(shardPath, key+".body"))
				continue
			}
			for _, victim := range s.lru.admit(key, m.Size) {
				s.removeFiles(victim)
			}
		}
		// .body files with no matching .meta -> orphan (age-gated reap).
		for key := range bodies {
			if _, ok := seenMeta[key]; !ok {
				reapIfStale(filepath.Join(shardPath, key+".body"), now)
			}
		}
	}
}

// reapIfStale deletes path only if its mtime is at least reapMinAge old, so a
// concurrent in-flight commit is never deleted.
func reapIfStale(path string, now time.Time) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	if now.Sub(fi.ModTime()) >= reapMinAge {
		os.Remove(path)
	}
}

// atomicWriteRename writes data to tmpPath (fsync'd) then renames to path.
func atomicWriteRename(path string, data []byte, tmpPath string) error {
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
