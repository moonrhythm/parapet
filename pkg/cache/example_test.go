package cache_test

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/cache"
)

// Mount a disk-backed response cache ahead of the upstream whose responses it
// should cache.
func ExampleNew() {
	// 1 GiB on disk, surviving restarts; use cache.NewMemory(size) for an in-memory
	// cache held in RAM instead.
	store, err := cache.NewDisk("/var/cache/app", 1<<30)
	if err != nil {
		log.Fatal(err)
	}

	s := parapet.New()
	s.Use(cache.New(store, cache.Options{
		MaxFileSize:  8 << 20, // don't cache bodies larger than 8 MiB
		DecoupleFill: true,    // a slow client won't stall waiting followers
	}))
	// s.Use(upstream.SingleHost(...)) — the handler whose responses get cached.
}

// Make the cache observable: record the per-request outcome (HIT/MISS/STALE/
// STALE_ERROR/BYPASS) as a "cacheStatus" field in the structured access log. Pair
// with prom.Cache() for Prometheus metrics (see that function's example).
func ExampleLogResult() {
	cache.New(cache.NewMemory(256<<20), cache.Options{
		OnResult: cache.LogResult,
	})
}

// Force caching for an origin that sends no cache headers, deciding on both the
// request and the origin's response — here, only successful image responses.
func ExampleOverride() {
	cache.New(cache.NewMemory(256<<20), cache.Options{
		Override: func(r *http.Request, status int, header http.Header) *cache.Override {
			if status == http.StatusOK && strings.HasPrefix(header.Get("Content-Type"), "image/") {
				return &cache.Override{TTL: time.Hour} // OverrideBalanced by default
			}
			return nil // everything else: respect the origin's Cache-Control
		},
	})
}
