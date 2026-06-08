// Package purge invalidates entries in a pkg/cache response cache.
//
// A Table is a small in-memory record of "everything cached at or before epoch T
// is invalid", consulted at cache-lookup time through the
// cache.Options.InvalidatedAfter hook. Invalidation is LAZY: issuing a purge is
// O(1) (one map write stamping the wall clock) and a purged entry is physically
// reclaimed only on its next lookup — exactly when the hook reports it stale, like
// a passed FreshUntil. Correctness is immediate (a purged entry can never be
// served); the storage backend's LRU byte cap reclaims space regardless, and the
// optional Reap sweep reclaims proactively.
//
//	pt := purge.New()
//	c := cache.New(store, cache.Options{InvalidatedAfter: pt.InvalidatedAfter})
//	...
//	pt.PurgeURL("example.com", "/a")   // invalidate one URL (all methods/schemes/variants)
//	pt.PurgeTag("product-42")          // invalidate by surrogate key (Cache-Tag), any host
//	go func() { for range time.Tick(5 * time.Minute) { pt.Reap(store) } }()
//
// A Table persists nothing on its own; Snapshot/Restore serialize its state so a
// caller can keep purges across restarts however it likes.
package purge

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moonrhythm/parapet/pkg/cache"
)

// defaultMaxRecords bounds each scope map. Purge records come from operator-issued
// purges (not per request), so this is generous; on overflow the map folds into
// the global epoch (conservative over-invalidation, never under-invalidation),
// keeping memory finite without a reaper.
const defaultMaxRecords = 1 << 16

// Table is a cache-invalidation record set, consulted via InvalidatedAfter.
//
// Scopes, checked as a max at lookup so a URL is also covered by its host's purge,
// a matching path prefix, and a global flush:
//   - global  — FlushAll: everything (one epoch).
//   - host    — PurgeHost: every URL under a host (keyed by normalized host).
//   - url     — PurgeURL: one URL across all methods, schemes, and Vary variants
//     (keyed by hash(host ⊕ uri), so a single purge of /a covers GET+HEAD,
//     http+https, and every cached variant).
//   - prefix  — PurgePrefix: every URL under a path prefix on a host (path-only,
//     boundary-aware: /blog matches /blog and /blog/x but not /blogger; query
//     strings ignored). A linear scan of the host's prefix records.
//   - tag     — PurgeTag: every cached response carrying a surrogate key (the
//     origin's Cache-Tag header, stored in cache.Meta.Tags); host-independent.
//
// Epochs are monotonic non-decreasing: every stamp is clamped to be >= the
// largest epoch ever issued, so a wall-clock step back (NTP correction) can never
// lower an epoch and "un-purge" entries. Safe for concurrent use.
type Table struct {
	host   map[string]int64       // normalized host  -> epoch
	url    map[string]int64       // hash(host ⊕ uri) -> epoch
	prefix map[string][]PrefixRec // normalized host  -> path-prefix records
	tag    map[string]int64       // surrogate key    -> epoch (host-independent)
	now    func() time.Time       // injectable clock; nil => time.Now

	global    int64  // flush-all epoch (unix nanos); 0 = never flushed
	highWater int64  // largest epoch ever stamped (monotonic clamp)
	folds     uint64 // count of conservative cap-folds (for Stats)
	maxRecs   int

	mu sync.RWMutex

	// active is true once any purge has ever been stamped (or restored). The
	// serving-path hook reads it lock-free FIRST and returns 0 immediately when
	// false — so a cache that has never been purged pays nothing per hit (no lock,
	// no key hash, no scans). It only ever flips false->true.
	active atomic.Bool
}

// PrefixRec is one path-prefix purge for a host: the normalized prefix (trailing
// slash trimmed; "" means the whole host) and its epoch. Exported so it round-trips
// through Snapshot.
type PrefixRec struct {
	Prefix string `json:"prefix"`
	Epoch  int64  `json:"epoch"`
}

// Option configures a Table.
type Option func(*Table)

// WithMaxRecords caps each scope map before it folds into the global epoch.
// Values <= 0 keep the default (65536).
func WithMaxRecords(n int) Option {
	return func(t *Table) {
		if n > 0 {
			t.maxRecs = n
		}
	}
}

// WithClock overrides the clock used to stamp epochs (mainly for tests).
func WithClock(now func() time.Time) Option {
	return func(t *Table) { t.now = now }
}

// New creates an empty Table.
func New(opts ...Option) *Table {
	t := &Table{
		host:    map[string]int64{},
		url:     map[string]int64{},
		prefix:  map[string][]PrefixRec{},
		tag:     map[string]int64{},
		maxRecs: defaultMaxRecords,
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

func (t *Table) nowNanos() int64 {
	if t.now != nil {
		return t.now().UnixNano()
	}
	return time.Now().UnixNano()
}

// FlushAll invalidates everything cached at the call instant. It also clears the
// per-scope maps (now redundant: the global epoch supersedes any record <= it),
// reclaiming their memory.
func (t *Table) FlushAll() {
	t.mu.Lock()
	t.global = t.stamp()
	t.host = map[string]int64{}
	t.url = map[string]int64{}
	t.prefix = map[string][]PrefixRec{}
	t.tag = map[string]int64{}
	t.mu.Unlock()
}

// PurgeHost invalidates every URL cached under host. The host is normalized
// (lowercased, port-stripped) to match the cache key.
func (t *Table) PurgeHost(host string) {
	h := normHost(host)
	if h == "" {
		return
	}
	t.mu.Lock()
	t.host[h] = t.stamp()
	t.enforceCapLocked()
	t.mu.Unlock()
}

// PurgeURL invalidates one URL on host across all methods, schemes, and Vary
// variants. uri is the request-uri (path+query); a different query is a different
// URL.
func (t *Table) PurgeURL(host, uri string) {
	h := normHost(host)
	if h == "" {
		return
	}
	t.mu.Lock()
	t.url[urlKey(h, uri)] = t.stamp()
	t.enforceCapLocked()
	t.mu.Unlock()
}

// PurgePrefix invalidates every URL under a path prefix on host, on a path
// boundary: "/blog" matches "/blog" and "/blog/x" but not "/blogger". A trailing
// slash is normalized away; "/" purges the whole host. Query strings are ignored.
func (t *Table) PurgePrefix(host, prefix string) {
	h := normHost(host)
	if h == "" {
		return
	}
	t.mu.Lock()
	t.applyPrefixLocked(h, normalizePrefix(prefix), t.stamp())
	t.enforceCapLocked()
	t.mu.Unlock()
}

// PurgeTag invalidates every cached response carrying the surrogate key tag (from
// the origin's Cache-Tag header, stored in cache.Meta.Tags), across all hosts.
func (t *Table) PurgeTag(tag string) {
	if tag == "" {
		return
	}
	t.mu.Lock()
	t.tag[tag] = t.stamp()
	t.enforceCapLocked()
	t.mu.Unlock()
}

// InvalidatedAfter is the cache.Options.InvalidatedAfter hook: it returns the
// invalidation epoch (unix nanos) applying to r — the max of the global, per-host,
// per-url, per-prefix, and (using the stored entry's surrogate keys) per-tag
// epochs. The cache treats a hit whose Meta.Created is <= this value as stale.
func (t *Table) InvalidatedAfter(r *http.Request, m cache.Meta) int64 {
	return t.epochFor(normHost(r.Host), r.URL.RequestURI(), m.Tags)
}

// InvalidatedAfterMeta is the off-request variant used by Reap: it reads the
// (already-normalized) host + uri from a stored entry's Meta instead of a live
// request. An entry with an empty Host matches only the global scope.
func (t *Table) InvalidatedAfterMeta(m cache.Meta) int64 {
	return t.epochFor(normHost(m.Host), m.URI, m.Tags)
}

// epochFor returns the invalidation epoch applying to an entry: the max across all
// scopes. Shared by the lookup hook and the reaper.
func (t *Table) epochFor(host, uri string, tags []string) int64 {
	if !t.active.Load() {
		return 0 // no purge ever issued: skip the lock, the key hash, and the scans
	}
	uk := urlKey(host, uri)
	path := pathOf(uri)
	t.mu.RLock()
	defer t.mu.RUnlock()
	e := t.global
	if v := t.host[host]; v > e {
		e = v
	}
	if v := t.url[uk]; v > e {
		e = v
	}
	// Prefix scope: linear scan of this host's records (epoch check first so the
	// string match is skipped once a higher epoch is already found).
	for _, p := range t.prefix[host] {
		if p.Epoch > e && matchPrefix(path, p.Prefix) {
			e = p.Epoch
		}
	}
	// Tag scope: each surrogate key the entry carries (host-independent).
	for _, tg := range tags {
		if v := t.tag[tg]; v > e {
			e = v
		}
	}
	return e
}

// applyPrefixLocked stamps (or refreshes, in place) a host's path-prefix record.
// Caller holds t.mu.
func (t *Table) applyPrefixLocked(host, prefix string, epoch int64) {
	recs := t.prefix[host]
	for i := range recs {
		if recs[i].Prefix == prefix {
			if epoch > recs[i].Epoch {
				recs[i].Epoch = epoch
			}
			return
		}
	}
	t.prefix[host] = append(recs, PrefixRec{Prefix: prefix, Epoch: epoch})
}

// stamp returns a fresh epoch, clamped to be monotonic non-decreasing (>=
// highWater) so a wall-clock step back can't un-purge, and opens the active gate.
// Caller holds t.mu.
func (t *Table) stamp() int64 {
	n := t.nowNanos()
	if n < t.highWater {
		n = t.highWater
	}
	t.highWater = n
	t.active.Store(true)
	return n
}

// enforceCapLocked keeps each map within maxRecs by folding an overflowing map
// into the global epoch (which jumps to highWater, covering every record purged so
// far) and clearing it. Over-invalidates (a coarser flush) but never
// under-invalidates, bounding memory without a reaper. Caller holds t.mu.
func (t *Table) enforceCapLocked() {
	folded := false
	if len(t.url) > t.maxRecs {
		t.url = map[string]int64{}
		folded = true
	}
	if len(t.host) > t.maxRecs {
		t.host = map[string]int64{}
		folded = true
	}
	if t.prefixCount() > t.maxRecs {
		t.prefix = map[string][]PrefixRec{}
		folded = true
	}
	if len(t.tag) > t.maxRecs {
		t.tag = map[string]int64{}
		folded = true
	}
	if folded {
		t.global = t.highWater
		t.folds++
	}
}

// prefixCount totals the prefix records across hosts. Caller holds t.mu.
func (t *Table) prefixCount() int {
	n := 0
	for _, recs := range t.prefix {
		n += len(recs)
	}
	return n
}

// Stats is a concurrent-safe snapshot of the table size, for metrics/diagnostics.
type Stats struct {
	Global     int64
	HostRecs   int
	URLRecs    int
	PrefixRecs int
	TagRecs    int
	Folds      uint64
}

// Stats returns a snapshot of the table's record counts.
func (t *Table) Stats() Stats {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return Stats{
		Global:     t.global,
		HostRecs:   len(t.host),
		URLRecs:    len(t.url),
		PrefixRecs: t.prefixCount(),
		TagRecs:    len(t.tag),
		Folds:      t.folds,
	}
}

// Snapshot is a serializable copy of a Table's invalidation state, for
// persistence. It is JSON-marshalable; restore it with Table.Restore.
type Snapshot struct {
	Host   map[string]int64       `json:"host,omitempty"`
	URL    map[string]int64       `json:"url,omitempty"`
	Prefix map[string][]PrefixRec `json:"prefix,omitempty"`
	Tag    map[string]int64       `json:"tag,omitempty"`
	Global int64                  `json:"global,omitempty"`
}

// Snapshot returns an independent, marshalable copy of the table's state, so the
// caller can persist it (e.g. fsync to disk) off the serving path.
func (t *Table) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return Snapshot{
		Global: t.global,
		Host:   cloneInt64Map(t.host),
		URL:    cloneInt64Map(t.url),
		Prefix: clonePrefixMap(t.prefix),
		Tag:    cloneInt64Map(t.tag),
	}
}

// Restore replaces the table's state with a snapshot's (deep-copied), recomputing
// the monotonic high-water mark and the active gate so a restored purge keeps
// gating after a restart. Call it before serving, not concurrently with purges.
func (t *Table) Restore(s Snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.host = cloneInt64Map(s.Host)
	t.url = cloneInt64Map(s.URL)
	t.prefix = clonePrefixMap(s.Prefix)
	t.tag = cloneInt64Map(s.Tag)
	t.global = s.Global
	hw := s.Global
	for _, v := range t.host {
		if v > hw {
			hw = v
		}
	}
	for _, v := range t.url {
		if v > hw {
			hw = v
		}
	}
	for _, recs := range t.prefix {
		for _, p := range recs {
			if p.Epoch > hw {
				hw = p.Epoch
			}
		}
	}
	for _, v := range t.tag {
		if v > hw {
			hw = v
		}
	}
	t.highWater = hw
	if hw > 0 {
		t.active.Store(true)
	}
}

func cloneInt64Map(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func clonePrefixMap(m map[string][]PrefixRec) map[string][]PrefixRec {
	out := make(map[string][]PrefixRec, len(m))
	for k, v := range m {
		out[k] = append([]PrefixRec(nil), v...) // PrefixRec is a value type
	}
	return out
}

// --- keys ---

// normHost lowercases and strips the port from a host, mirroring the cache's key
// derivation exactly so a purge key matches a stored entry. It does NOT strip a
// trailing dot (neither does the cache).
func normHost(host string) string {
	h := strings.ToLower(host)
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	return h
}

// urlKey is the per-url map key: hash(host ⊕ uri). Hashing host⊕uri (not
// method/scheme) makes one url purge cover GET+HEAD, http+https, and every Vary
// variant at once.
func urlKey(host, uri string) string {
	sum := sha256.Sum256([]byte(host + "\n" + uri))
	return hex.EncodeToString(sum[:16])
}

// pathOf returns the path portion of a request-uri (everything before the first
// '?'), so a query string never affects prefix matching.
func pathOf(uri string) string {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		return uri[:i]
	}
	return uri
}

// normalizePrefix trims a single trailing slash so "/blog" and "/blog/" purge the
// same section; "/" normalizes to "" (the whole-host prefix).
func normalizePrefix(p string) string {
	return strings.TrimRight(p, "/")
}

// matchPrefix reports whether path is covered by the normalized prefix pre, on a
// path boundary: "/blog" matches "/blog" and "/blog/x" but NOT "/blogger". An empty
// pre (a "/" purge) matches every path.
func matchPrefix(path, pre string) bool {
	if pre == "" {
		return true
	}
	return path == pre || strings.HasPrefix(path, pre+"/")
}
