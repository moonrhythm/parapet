// Package cache is an HTTP response-cache middleware with a pluggable storage
// backend. It implements a CDN-style honor-origin policy: it caches a response
// only when the origin opts in via explicit Cache-Control/Expires freshness;
// refuses private/no-store/no-cache, Set-Cookie, and Vary: *; honors Vary
// (keying per varied request header); GET/HEAD only; and ignores the client's
// request Cache-Control so a client can't bust the shared cache. Concurrent
// misses for one key collapse into a single origin fetch. It is fail-static: any
// storage error degrades to a cache miss, never an error to the client. The
// response is tagged X-Cache: HIT|MISS.
//
// Two storage backends ship: an in-memory one ([NewMemory], bodies held in RAM,
// lost on restart) and a disk-backed one ([NewDisk], survives restarts, streams
// bodies to disk so it isn't bounded by RSS). Both bound their total size with
// LRU eviction and a per-object cap. Plug either (or your own [Storage]) into
// [New].
//
//	store, _ := cache.NewDisk("/var/cache/app", 1<<30) // 1 GiB on disk
//	m := cache.New(store, cache.Options{MaxFileSize: 8 << 20})
//	srv.Use(m) // mount it ahead of the upstream/handler it should cache
//
// Only origin-opted-in (public, fresh) content is cached, so per-user responses
// must be marked uncacheable by the origin. As a shared cache it additionally
// follows RFC 9111 §3.5: a response to a request bearing an Authorization header
// is cached only when the origin explicitly opts in via public, s-maxage, or
// must-revalidate.
//
// The key is the request's host+method+scheme+uri plus the origin-declared Vary
// headers; it does not include request headers the origin reflects but doesn't
// declare in Vary (e.g. X-Forwarded-Host into a Location), so — as with any
// honor-origin shared cache — such a response can be poisoned. Use Options.Cacheable
// to exclude untrusted requests or paths from the cache.
package cache

import (
	"io"
	"net/http"
)

// Storage is the cache backend: where cached entries live and how total size is
// bounded. The middleware handles policy, keys, Vary, locking, and X-Cache;
// Storage only persists bytes and enforces capacity (LRU + per-object cap).
//
// Implementations must be safe for concurrent use BY A SINGLE Cache instance.
// One backing store (e.g. a [DiskStorage] dir) must be owned by one Cache:
// concurrent same-key writes are otherwise unsynchronized. The middleware
// serializes same-key fills with its fill lock and only writes a key it has just
// missed, so within one Cache the store never sees a same-key Get racing a Set.
type Storage interface {
	// Get returns the entry stored under key, or ok=false on a miss. A hit should
	// be counted as a recent use (LRU). The returned Meta (including its Header map
	// and Vary slice) must be independent of stored state — safe for the caller to
	// read or modify without affecting the cache; the returned body must not be
	// mutated. Any internal error is reported as a miss (fail-static).
	Get(key string) (meta Meta, body []byte, ok bool)

	// Writer begins storing an entry under key. The caller streams the body to the
	// returned EntryWriter and then calls Commit (to persist) or Abort (to
	// discard). It returns an error if a writer can't be opened (then the entry is
	// simply not cached). The disk backend streams to a temp file so the body
	// never has to be buffered whole in RAM.
	Writer(key string) (EntryWriter, error)

	// Delete removes the entry under key (e.g. when the middleware finds it stale).
	Delete(key string)

	// Range calls fn for each currently-stored entry (key + its Meta), stopping
	// early if fn returns false. It is for out-of-band maintenance — e.g. a purge
	// reaper that physically deletes entries matching an external predicate — not
	// the serving path. Iteration order is unspecified, and the snapshot is
	// best-effort: fn may observe an entry that is concurrently deleted, and an
	// entry written during the walk may or may not be visited. The Meta passed to
	// fn is independent of stored state (see Get), so fn may freely read or modify
	// it. fn MAY call Delete(key) on this storage (a backend must not hold a lock
	// across fn that Delete would need). fn MUST NOT call Writer.
	Range(fn func(key string, m Meta) bool)
}

// EntryWriter streams one cached body and finalizes it. Exactly one of Commit or
// Abort must be called; after either, the writer is spent. Abort after Commit (or
// vice versa) is a no-op. Backends admit the entry to their capacity bound (LRU)
// inside Commit.
type EntryWriter interface {
	io.Writer
	// Commit persists the streamed body with meta and admits it to the byte cap
	// (evicting LRU victims). A failure is fail-static: the entry is not cached.
	Commit(meta Meta) error
	// Abort discards the streamed body (e.g. truncated/over-cap/panicked fill).
	Abort()
}

// Meta is the stored metadata for a cached response. Backends persist it
// alongside the body (the disk backend marshals it to JSON).
type Meta struct {
	Header     http.Header `json:"header"`
	PrimaryHex string      `json:"primary"`        // primary key hash (host+method+scheme+uri)
	Host       string      `json:"host,omitempty"` // normalized host (lowercased, port-stripped); for out-of-band Range maintenance
	URI        string      `json:"uri,omitempty"`  // request-uri (path+query); for out-of-band Range maintenance
	Vary       []string    `json:"vary"`           // lowercased Vary header names
	Tags       []string    `json:"tags,omitempty"` // surrogate keys from the response Cache-Tag header; for out-of-band tag-scoped Range maintenance
	Created    int64       `json:"created"`        // unix nanos
	FreshUntil int64       `json:"fresh"`          // unix nanos; entry is stale after this
	Size       int64       `json:"size"`           // body bytes (== eviction weight)
	Status     int         `json:"status"`
}
