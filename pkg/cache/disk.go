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

// DiskStorage is a disk-backed cache backend. Entries survive restarts and are
// not bounded by RSS. The total on-disk byte cap is held by an in-memory LRU,
// re-seeded from disk by a background startup scan that also reaps orphans, torn
// writes, and expired entries. Safe for concurrent use.
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
// any miss / corruption / torn write (fail-static — treated as a cache miss).
func (s *DiskStorage) Get(key string) (Meta, []byte, bool) {
	mb, err := os.ReadFile(s.metaPath(key))
	if err != nil {
		return Meta{}, nil, false // clean miss or unreadable
	}
	var m Meta
	if err := json.Unmarshal(mb, &m); err != nil {
		return Meta{}, nil, false // corrupt sidecar
	}
	body, err := os.ReadFile(s.bodyPath(key))
	if err != nil {
		return Meta{}, nil, false // meta without body (torn)
	}
	s.lru.touch(key)
	return m, body, true
}

// Set writes body+meta under key: body to a temp file, fsync, atomically rename
// to <key>.body, then atomically write <key>.meta LAST (so its presence implies
// a complete body). On success it admits to the byte cap and evicts LRU victims.
// Any IO error is fail-static (the entry is simply not cached).
func (s *DiskStorage) Set(key string, meta Meta, body []byte) {
	tmp := s.tempPath(key)
	if err := atomicWrite(tmp, body); err != nil {
		os.Remove(tmp)
		return
	}
	if err := os.MkdirAll(s.shardDir(key), 0o755); err != nil {
		os.Remove(tmp)
		return
	}
	bodyPath := s.bodyPath(key)
	if err := os.Rename(tmp, bodyPath); err != nil {
		os.Remove(tmp)
		return
	}
	mb, err := json.Marshal(meta)
	if err != nil {
		os.Remove(bodyPath)
		return
	}
	if err := atomicWriteRename(s.metaPath(key), mb, s.tempPath(key+".meta")); err != nil {
		os.Remove(bodyPath) // roll back so a body without meta isn't left
		return
	}
	for _, victim := range s.lru.admit(key, meta.Size) {
		s.removeFiles(victim)
	}
}

// Delete removes the entry under key (meta first, so a half-deleted entry reads
// as a clean miss rather than a torn write) and its LRU accounting.
func (s *DiskStorage) Delete(key string) {
	s.removeFiles(key)
	s.lru.remove(key)
}

func (s *DiskStorage) removeFiles(key string) {
	os.Remove(s.metaPath(key))
	os.Remove(s.bodyPath(key))
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

// atomicWrite writes data to path, fsyncing before close (path is a temp file
// the caller renames into place).
func atomicWrite(path string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// atomicWriteRename writes data to tmpPath (fsync'd) then renames to path.
func atomicWriteRename(path string, data []byte, tmpPath string) error {
	if err := atomicWrite(tmpPath, data); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
