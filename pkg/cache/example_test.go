package cache_test

import (
	"log"

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
