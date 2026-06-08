package purge

import "github.com/moonrhythm/parapet/pkg/cache"

// Reap sweeps storage once, PHYSICALLY deleting every entry the table has
// invalidated (Meta.Created <= the entry's invalidation epoch), and returns the
// number deleted.
//
// It complements the lazy lookup gate (InvalidatedAfter), which reaps an entry
// only when it is next looked up — so after a broad purge with little subsequent
// traffic the dead bytes would linger until LRU pressure evicts them. Reap
// reclaims them proactively. Correctness never depends on it (the gate already
// guarantees a purged entry is never served); it is pure reclamation, and
// over-deleting a still-valid entry only costs a re-fetch, never a stale serve.
//
// Run it periodically off the serving path:
//
//	go func() { for range time.Tick(5 * time.Minute) { pt.Reap(store) } }()
//
// Reap deliberately does NOT retire purge records — that is the one
// under-invalidating direction, unsafe against a backward wall-clock step between a
// purge and a later fill's commit. The table's memory is instead bounded by the
// monotonic, over-invalidating cap-fold (WithMaxRecords).
func (t *Table) Reap(storage cache.Storage) int {
	var reaped int
	storage.Range(func(key string, m cache.Meta) bool {
		if m.Created <= t.InvalidatedAfterMeta(m) {
			storage.Delete(key)
			reaped++
		}
		return true
	})
	return reaped
}
