package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moonrhythm/parapet"
)

// Cache is a parapet.Middleware.
var _ parapet.Middleware = (*Cache)(nil)

// defaultLockTimeout bounds how long a concurrent miss waits for the in-flight
// fill (the "leader") to populate the cache before fetching on its own. Used when
// Options.LockTimeout <= 0.
const defaultLockTimeout = 2 * time.Second

// defaultMaxFileSize is the per-object cap when Options.MaxFileSize <= 0.
const defaultMaxFileSize = 8 << 20 // 8 MiB

// defaultRevalidateTimeout bounds a background stale-while-revalidate fetch when
// Options.RevalidateTimeout <= 0.
const defaultRevalidateTimeout = 30 * time.Second

// maxPrimaryVary bounds the in-memory primary->Vary map so a long-tail URL space
// can't grow it without limit (the storage backend bounds bytes, not this map).
// When the cap is hit the map is reset; a dropped entry just costs one re-learn
// (the next fill re-records its Vary), so correctness is unaffected.
var maxPrimaryVary = 1 << 16

// Options configures the cache middleware.
type Options struct {
	// InvalidatedAfter, when non-nil, is consulted on every cache hit to support
	// out-of-band invalidation (cache purge). It receives the request and the
	// stored entry's Meta and returns an invalidation epoch in unix nanos: a hit
	// whose Meta.Created is <= the returned epoch is treated as stale (reaped and
	// served as a miss), exactly like a passed FreshUntil. Return 0 (or any value
	// below the entry's Created) to keep the entry. It runs only on a hit, so it
	// costs nothing while the cache is idle; nil disables the check entirely (zero
	// overhead). The callee owns its own concurrency.
	InvalidatedAfter func(r *http.Request, m Meta) int64

	// Cacheable, when non-nil, is called for each GET/HEAD request; returning false
	// excludes the request from the cache entirely (served straight from the origin,
	// untagged), exactly as if it were uncacheable. Use it to restrict caching to
	// vetted paths, or to refuse requests carrying headers an origin might reflect
	// into the body without declaring them in Vary (an unkeyed-input poisoning
	// vector — see the package doc). nil caches everything otherwise cacheable.
	Cacheable func(r *http.Request) bool

	// MaxFileSize caps a cacheable response's body. A GET response larger than
	// this (by Content-Length, or mid-stream) is not cached but still served in
	// full. Defaults to 8 MiB when <= 0.
	MaxFileSize int64

	// LockTimeout bounds how long a concurrent miss (a "follower") waits for the
	// in-flight leader to populate the cache before giving up and fetching the
	// origin itself. The leader holds the fill lock for the WHOLE time it streams
	// the response to its own client and (on the disk backend) fsyncs the committed
	// entry, so a slow leader client or a saturated disk can hold followers for up
	// to this long before they fall back to the origin. Raise it to wait through a
	// slow fill (fewer origin fetches, higher follower latency); lower it to fail
	// fast (more origin fetches under load). Defaults to 2s when <= 0.
	LockTimeout time.Duration

	// RevalidateTimeout bounds a background stale-while-revalidate fetch (RFC
	// 5861): the detached request to the origin is cancelled after this long so a
	// hung origin can't pin the single-flight lock or leak a goroutine. It does
	// not apply to a normal (foreground) fill. Defaults to 30s when <= 0.
	RevalidateTimeout time.Duration

	// DefaultStaleWhileRevalidate and DefaultStaleIfError force RFC 5861 stale
	// serving for a cacheable response that does not carry the matching directive,
	// so an origin you don't control still gets stale-while-revalidate /
	// stale-if-error behavior. An explicit directive on the response wins; a
	// response marked must-revalidate / proxy-revalidate is never served stale,
	// regardless of these. Unlike injecting the directive with a headers
	// middleware, these stay private to this cache: the served Cache-Control is
	// the origin's, so the policy does not propagate to downstream clients/caches.
	// Zero (the default) forces nothing. Each is clamped to ~10y.
	DefaultStaleWhileRevalidate time.Duration
	DefaultStaleIfError         time.Duration

	// DecoupleFill, when true, stops a slow leader client (or a slow disk) from
	// holding the fill lock — and thus stalling waiting followers — while its response
	// streams to that client. It engages only when the fill is CONTENDED, i.e. at
	// least one follower is already blocked waiting for it (the stampede case
	// single-flight exists for); an uncontended fill has nothing to isolate and
	// streams to the client in lockstep with no added latency.
	//
	// When it engages, the cacheable body is read from the origin at origin speed —
	// the leader's client receives nothing until the fill is done — while it is
	// streamed to storage and also buffered in memory for the leader (so the leader's
	// response never depends on the stored entry, which may expire, be evicted, or be
	// purged). The entry is then committed, the fill lock released (waiting followers
	// immediately hit the cache), and only then is the leader's own client served from
	// the buffered body. This trades the leader's time-to-first-byte (it waits for the
	// whole fill) and an extra in-memory copy of the leader's body (bounded by
	// MaxFileSize, per in-flight decoupled fill) for follower isolation, and serves
	// the leader the sanitized response headers (hop-by-hop stripped, no Age) rather
	// than the raw origin headers. A handler that relies on incremental flushing is
	// unaffected (a Flush during a decoupled fill is ignored). When false (default),
	// the leader always streams in lockstep and holds the fill lock until that stream
	// and the commit finish (see LockTimeout).
	DecoupleFill bool
}

// Cache is the HTTP response-cache middleware. It implements parapet.Middleware
// (ServeHandler). Construct with New, giving it a Storage backend.
type Cache struct {
	storage          Storage
	invalidatedAfter func(r *http.Request, m Meta) int64
	cacheable        func(r *http.Request) bool
	primaryVary      map[string][]string  // primaryHex -> Vary header names learned from a stored response
	locks            map[string]*fillLock // variantHex -> in-flight fill
	maxFileSize       int64
	lockTimeout       time.Duration
	revalidateTimeout time.Duration
	defaultSWR        time.Duration // Options.DefaultStaleWhileRevalidate, clamped
	defaultSIE        time.Duration // Options.DefaultStaleIfError, clamped
	decoupleFill      bool

	pvMu sync.RWMutex

	lockMu sync.Mutex
}

// fillLock coordinates concurrent misses for one variant: the leader fills, the
// rest wait on done (then re-read the cache) or time out and fetch on their own.
// waiters counts the followers currently blocked on done — the leader reads it to
// decide whether a fill is contended (see DecoupleFill).
type fillLock struct {
	done    chan struct{}
	waiters atomic.Int32
}

// New builds the cache middleware over the given Storage backend (memory or
// disk).
func New(storage Storage, opts Options) *Cache {
	mfs := opts.MaxFileSize
	if mfs <= 0 {
		mfs = defaultMaxFileSize
	}
	lt := opts.LockTimeout
	if lt <= 0 {
		lt = defaultLockTimeout
	}
	rt := opts.RevalidateTimeout
	if rt <= 0 {
		rt = defaultRevalidateTimeout
	}
	return &Cache{
		storage:           storage,
		maxFileSize:       mfs,
		lockTimeout:       lt,
		revalidateTimeout: rt,
		defaultSWR:        clampStaleWindow(opts.DefaultStaleWhileRevalidate),
		defaultSIE:        clampStaleWindow(opts.DefaultStaleIfError),
		invalidatedAfter:  opts.InvalidatedAfter,
		cacheable:         opts.Cacheable,
		decoupleFill:      opts.DecoupleFill,
		primaryVary:       map[string][]string{},
		locks:             map[string]*fillLock{},
	}
}

// ServeHandler implements parapet.Middleware: it wraps next (the
// upstream/handler whose responses are cached). A hit short-circuits next; a
// miss fetches via next and stores.
func (c *Cache) ServeHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.serve(w, r, next)
	})
}

func (c *Cache) serve(w http.ResponseWriter, r *http.Request, next http.Handler) {
	// Range requests are passed straight through: this cache has no Range support,
	// so it must not answer a Range request with a stored full 200 (let the origin
	// serve 206). They are also not used to fill the cache.
	if !cacheableMethod(r.Method) || isUpgrade(r) || r.Header.Get("Range") != "" || (c.cacheable != nil && !c.cacheable(r)) {
		next.ServeHTTP(w, r) // never cache these; no X-Cache header
		return
	}
	primaryHex := c.primaryHash(r)
	key := c.variantHash(primaryHex, r)
	if m, body, ok := c.storage.Get(key); ok {
		switch c.classify(m, r, time.Now()) {
		case stateFresh:
			writeStored(w, r, m, body, "HIT")
			return
		case stateStaleRevalidate:
			// RFC 5861 stale-while-revalidate: serve the stale entry now and refresh
			// it in the background (single-flighted), so the client never waits on the
			// origin.
			writeStored(w, r, m, body, "STALE")
			c.revalidate(r, next, primaryHex)
			return
		case stateStaleIfError:
			// RFC 5861 stale-if-error: try the origin, but fall back to this stale
			// entry if the revalidation returns a server error.
			c.fillWithStale(w, r, next, primaryHex, m, body)
			return
		case stateExpired:
			c.storage.Delete(key)
		}
	}
	c.fillAndServe(w, r, next, primaryHex)
}

// tryServeHit serves key from storage if present and fresh, returning true. It is
// the follower re-read after a fill: an expired entry is reaped and reported as a
// miss, while a stale-but-still-serveable entry (within an RFC 5861 window) is
// kept (so stale-if-error can still fall back to it) and reported as a miss here.
// Fail-static: a storage error reads as a miss.
func (c *Cache) tryServeHit(w http.ResponseWriter, r *http.Request, key string) bool {
	m, body, ok := c.storage.Get(key)
	if !ok {
		return false
	}
	switch c.classify(m, r, time.Now()) {
	case stateFresh:
		writeStored(w, r, m, body, "HIT")
		return true
	case stateExpired:
		c.storage.Delete(key)
	}
	return false
}

// fillAndServe handles a miss with single-flight. The first arrival for a variant
// becomes the leader and fills the cache while streaming to its own client;
// concurrent arrivals wait for it (then re-read from cache) or time out and fetch
// on their own. Each tags X-Cache accurately (HIT when served from the just-filled
// cache, MISS when it contacted the origin).
//
// A primary's Vary is learned lazily, so before the first fill every variant of a
// URL shares one lock key. A follower whose varied-header values differ from the
// leader's therefore wakes to a miss; it then re-enters once to lead (or join) the
// single-flight for ITS OWN variant key — so each variant collapses to a single
// origin fetch and is cached, instead of every differing follower fetching
// uncached. The loop runs at most twice: once on the pre-Vary key, once on the
// learned-Vary key (which is stable thereafter).
func (c *Cache) fillAndServe(w http.ResponseWriter, r *http.Request, next http.Handler, primaryHex string) {
	for attempt := 0; attempt < 2; attempt++ {
		variantHex := c.variantHash(primaryHex, r)
		lock, leader := c.acquire(variantHex)
		if leader {
			c.fill(w, r, next, primaryHex, variantHex, lock)
			return
		}
		lock.waiters.Add(1)
		select {
		case <-lock.done:
		case <-time.After(c.lockTimeout):
		}
		lock.waiters.Add(-1)
		// Leader finished (or we timed out). Re-read: it may have just learned this
		// primary's Vary, so our key now matches the stored entry IF our varied
		// values match the leader's. A wrong-Vary variant is never served.
		if c.tryServeHit(w, r, c.variantHash(primaryHex, r)) {
			return
		}
		// Still a miss. If the leader learned a Vary we differ on, our key changed —
		// loop to lead/join the fill for our own variant. If the key is unchanged
		// (the leader didn't cache it, or we timed out before any Vary was learned),
		// fall through to our own uncached fetch.
		if c.variantHash(primaryHex, r) == variantHex {
			break
		}
	}
	w.Header().Set("X-Cache", "MISS")
	next.ServeHTTP(w, r)
}

// fill is the leader path: stream the response to the client through a teeWriter
// that also writes the cacheable body to storage, commit on completion, then
// release the fill lock so waiting followers find the committed entry.
func (c *Cache) fill(w http.ResponseWriter, r *http.Request, next http.Handler, primaryHex, variantHex string, lock *fillLock) {
	released := false
	release := func() {
		if !released {
			released = true
			c.release(variantHex, lock)
		}
	}
	defer release() // ensure the lock is freed on every path (incl. panic)

	tw := &teeWriter{rw: w, r: r, c: c, method: r.Method, primaryHex: primaryHex, lock: lock}
	defer tw.cleanup() // panic-safe: abort an uncommitted entry if finish never ran
	next.ServeHTTP(tw, r)
	tw.finish()

	// DecoupleFill: the cacheable body was streamed to storage, not the client, so
	// release the lock now (waiting followers find the committed entry) and only then
	// serve this leader's own — possibly slow — client from the committed entry. In
	// lockstep mode the client already has the full response; release runs via defer.
	if tw.deferredClient {
		release()
		tw.serveLeader()
	}
}

func (c *Cache) acquire(variantHex string) (*fillLock, bool) {
	c.lockMu.Lock()
	defer c.lockMu.Unlock()
	if l, ok := c.locks[variantHex]; ok {
		return l, false
	}
	l := &fillLock{done: make(chan struct{})}
	c.locks[variantHex] = l
	return l, true
}

func (c *Cache) release(variantHex string, l *fillLock) {
	c.lockMu.Lock()
	delete(c.locks, variantHex)
	c.lockMu.Unlock()
	close(l.done)
}

// primaryHash keys on host + method + scheme + uri (so distinct hosts/schemes/
// methods never collide). The host is lowercased and port-stripped so
// "example.com" and "example.com:443" share a key regardless of upstream host
// normalization. scheme is canonicalized to http/https by schemeOf.
func (c *Cache) primaryHash(r *http.Request) string {
	scheme := schemeOf(r)
	host := normalizeHost(r.Host)
	uri := r.URL.RequestURI()
	sum := sha256.Sum256([]byte(host + "\n" + r.Method + "\n" + scheme + "\n" + uri))
	return hex.EncodeToString(sum[:16])
}

// schemeOf returns the request scheme for the cache key, canonicalized to exactly
// "http" or "https". X-Forwarded-Proto is honored only when it is exactly http or
// https (case-insensitive); any other value — including attacker-supplied junk
// meant to fragment the key — falls back to the TLS state. Mount the cache behind
// a proxy that sets X-Forwarded-Proto from a trusted source.
func schemeOf(r *http.Request) string {
	switch {
	case strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"):
		return "https"
	case strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "http"):
		return "http"
	case r.TLS != nil:
		return "https"
	default:
		return "http"
	}
}

// normalizeHost lowercases a host and strips any port, matching the form used in
// the primary key. It is also stamped into Meta.Host so out-of-band Range
// maintenance can key on the same value.
func normalizeHost(host string) string {
	host = strings.ToLower(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h // drop :port (SplitHostPort errors when there is none)
	}
	return host
}

// variantHash mixes the primary hash with the request's values for the Vary
// header names learned for this primary, so distinct Vary variants get distinct
// entries. Before the primary's Vary is known the variance is empty — so the
// first fill stores under the actual response's Vary (see teeWriter.finish),
// which a later lookup then matches once the Vary map is learned.
func (c *Cache) variantHash(primaryHex string, r *http.Request) string {
	return variantHashFor(primaryHex, c.getPrimaryVary(primaryHex), r.Header)
}

// variantHashFor computes the storage key. names must be sorted + lowercased; the
// variance is each name's value in h. The same (primaryHex, names, h) on the
// lookup and store paths yields the same key.
func variantHashFor(primaryHex string, names []string, h http.Header) string {
	var b strings.Builder
	b.WriteString(primaryHex)
	b.WriteByte(0)
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(h.Get(name))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:16])
}

func (c *Cache) getPrimaryVary(primaryHex string) []string {
	c.pvMu.RLock()
	defer c.pvMu.RUnlock()
	return c.primaryVary[primaryHex]
}

func (c *Cache) setPrimaryVary(primaryHex string, names []string) {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	c.pvMu.Lock()
	// Bound the map by evicting one arbitrary entry when full — an O(1) re-learn,
	// rather than wiping the whole map (which would re-fill every varied URL at once).
	if _, exists := c.primaryVary[primaryHex]; !exists && len(c.primaryVary) >= maxPrimaryVary {
		for k := range c.primaryVary {
			delete(c.primaryVary, k)
			break
		}
	}
	c.primaryVary[primaryHex] = sorted
	c.pvMu.Unlock()
}

// writeStored writes a cached entry to the client. body is omitted for HEAD and
// bodiless statuses. X-Cache is set to tag (HIT/MISS).
func writeStored(w http.ResponseWriter, r *http.Request, m Meta, body []byte, tag string) {
	h := w.Header()
	for k, vs := range m.Header {
		h[k] = append([]string(nil), vs...)
	}
	h.Set("Age", strconv.FormatInt(servedAgeSeconds(m, time.Now()), 10))
	h.Set("X-Cache", tag)
	w.WriteHeader(m.Status)
	if r.Method == http.MethodHead || m.Status == http.StatusNoContent {
		return
	}
	_, _ = w.Write(body)
}

// servedAgeSeconds is the Age header value for a cache hit: the age the response
// already had when it was stored (RFC 9111 §4.2.3, reusing responseAge) plus how
// long it has since resided in the cache. Never negative.
func servedAgeSeconds(m Meta, now time.Time) int64 {
	created := time.Unix(0, m.Created)
	age := responseAge(m.Header, created) + now.Sub(created)
	if age < 0 {
		age = 0
	}
	return int64(age / time.Second)
}
