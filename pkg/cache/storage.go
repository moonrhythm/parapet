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
// lost on restart) and a disk-backed one ([NewDisk], survives restarts, bounded
// by an on-disk byte cap). Both bound their total size with LRU eviction and a
// per-object cap. Plug either (or your own [Storage]) into [New].
//
//	store, _ := cache.NewDisk("/var/cache/app", 1<<30) // 1 GiB on disk
//	m := cache.New(store, cache.Options{MaxFileSize: 8 << 20})
//	srv.Use(m) // mount it ahead of the upstream/handler it should cache
//
// Only origin-opted-in (public, fresh) content is cached, so per-user or
// authorization-sensitive responses must be marked uncacheable by the origin.
package cache

import "net/http"

// Storage is the cache backend: where cached entries live and how total size is
// bounded. The middleware handles policy, keys, Vary, locking, and X-Cache;
// Storage only persists bytes and enforces capacity (LRU + per-object cap).
//
// Implementations must be safe for concurrent use.
type Storage interface {
	// Get returns the entry stored under key, or ok=false on a miss. A hit should
	// be counted as a recent use (LRU). The returned body must not be mutated by
	// the caller. Any internal error is reported as a miss (fail-static).
	Get(key string) (meta Meta, body []byte, ok bool)

	// Set stores body+meta under key, admitting it to the capacity bound and
	// evicting least-recently-used entries as needed. body is the complete
	// response body (the middleware has already enforced the per-object cap). A
	// failure is best-effort/fail-static: the entry is simply not cached.
	Set(key string, meta Meta, body []byte)

	// Delete removes the entry under key (e.g. when the middleware finds it stale).
	Delete(key string)
}

// Meta is the stored metadata for a cached response. Backends persist it
// alongside the body (the disk backend marshals it to JSON).
//
//nolint:govet
type Meta struct {
	Status     int         `json:"status"`
	Header     http.Header `json:"header"`
	PrimaryHex string      `json:"primary"` // primary key hash (host+method+scheme+uri)
	Vary       []string    `json:"vary"`    // lowercased Vary header names
	Created    int64       `json:"created"` // unix nanos
	FreshUntil int64       `json:"fresh"`   // unix nanos; entry is stale after this
	Size       int64       `json:"size"`    // body bytes (== eviction weight)
}
