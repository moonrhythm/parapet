package purge_test

import (
	"time"

	"github.com/moonrhythm/parapet/pkg/cache"
	"github.com/moonrhythm/parapet/pkg/cache/purge"
)

// Wire a purge table into a cache, issue purges, and reclaim proactively.
func ExampleTable() {
	pt := purge.New()

	store := cache.NewMemory(256 << 20)
	_ = cache.New(store, cache.Options{
		InvalidatedAfter: pt.InvalidatedAfter, // the cache consults the table on every hit
	})

	// Issue purges when content changes — keyed on host, URL, path prefix, or
	// surrogate key (Cache-Tag). They take effect immediately (lazily).
	pt.PurgeURL("example.com", "/a")       // one URL: all methods, schemes, Vary variants
	pt.PurgePrefix("example.com", "/blog") // a whole section, boundary-aware
	pt.PurgeTag("product-42")              // every response carrying this surrogate key, any host
	pt.FlushAll()                          // everything

	// Optionally reclaim invalidated bytes proactively (correctness doesn't depend
	// on it — the lookup gate already prevents serving a purged entry).
	go func() {
		for range time.Tick(5 * time.Minute) {
			pt.Reap(store)
		}
	}()
}
