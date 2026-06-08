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

// Force caching for an origin that sends no cache headers, decided per request.
func ExampleOverride() {
	static := func(p string) bool {
		return strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".css") || strings.HasSuffix(p, ".jpg")
	}

	cache.New(cache.NewMemory(256<<20), cache.Options{
		Override: func(r *http.Request) *cache.Override {
			if r.Host == "static.example.com" && static(r.URL.Path) {
				return &cache.Override{TTL: time.Hour} // OverrideBalanced by default
			}
			return nil // every other request: respect the origin's Cache-Control
		},
	})
}
