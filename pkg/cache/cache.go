package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moonrhythm/parapet"
)

// Cache is a parapet.Middleware.
var _ parapet.Middleware = (*Cache)(nil)

// cacheLockTimeout bounds how long a concurrent miss waits for the in-flight fill
// (the "leader") to populate the cache before fetching on its own.
const cacheLockTimeout = 2 * time.Second

// defaultMaxFileSize is the per-object cap when Options.MaxFileSize <= 0.
const defaultMaxFileSize = 8 << 20 // 8 MiB

// maxPrimaryVary bounds the in-memory primary->Vary map so a long-tail URL space
// can't grow it without limit (the storage backend bounds bytes, not this map).
// When the cap is hit the map is reset; a dropped entry just costs one re-learn
// (the next fill re-records its Vary), so correctness is unaffected.
const maxPrimaryVary = 1 << 16

// Options configures the cache middleware.
type Options struct {
	// MaxFileSize caps a cacheable response's body. A GET response larger than
	// this (by Content-Length, or mid-stream) is not cached but still served in
	// full. Defaults to 8 MiB when <= 0.
	MaxFileSize int64
}

// Cache is the HTTP response-cache middleware. It implements parapet.Middleware
// (ServeHandler). Construct with New, giving it a Storage backend.
//
//nolint:govet
type Cache struct {
	storage     Storage
	maxFileSize int64

	pvMu        sync.RWMutex
	primaryVary map[string][]string // primaryHex -> Vary header names learned from a stored response

	lockMu sync.Mutex
	locks  map[string]*fillLock // variantHex -> in-flight fill
}

// fillLock coordinates concurrent misses for one variant: the leader fills, the
// rest wait on done (then re-read the cache) or time out and fetch on their own.
type fillLock struct {
	done chan struct{}
}

// New builds the cache middleware over the given Storage backend (memory or
// disk).
func New(storage Storage, opts Options) *Cache {
	mfs := opts.MaxFileSize
	if mfs <= 0 {
		mfs = defaultMaxFileSize
	}
	return &Cache{
		storage:     storage,
		maxFileSize: mfs,
		primaryVary: map[string][]string{},
		locks:       map[string]*fillLock{},
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
	if !cacheableMethod(r.Method) || isUpgrade(r) {
		next.ServeHTTP(w, r) // never cache these; no X-Cache header
		return
	}
	primaryHex := c.primaryHash(r)
	variantHex := c.variantHash(primaryHex, r)

	if c.tryServeHit(w, r, variantHex) {
		return
	}
	c.fillAndServe(w, r, next, primaryHex, variantHex)
}

// tryServeHit serves key from storage if present and fresh, returning true. An
// expired entry is reaped and reported as a miss (fail-static: a storage error
// reads as a miss).
func (c *Cache) tryServeHit(w http.ResponseWriter, r *http.Request, key string) bool {
	m, body, ok := c.storage.Get(key)
	if !ok {
		return false
	}
	if time.Now().After(time.Unix(0, m.FreshUntil)) {
		c.storage.Delete(key)
		return false
	}
	writeStored(w, r, m, body, "HIT")
	return true
}

// fillAndServe handles a miss. The first arrival becomes the leader and fills the
// cache while streaming to its own client; concurrent arrivals wait for the
// leader (then re-read from cache) or time out and fetch on their own. Each tags
// X-Cache accurately (HIT when served from the just-filled cache, MISS when it
// contacted the origin).
func (c *Cache) fillAndServe(w http.ResponseWriter, r *http.Request, next http.Handler, primaryHex, variantHex string) {
	lock, leader := c.acquire(variantHex)
	if !leader {
		select {
		case <-lock.done:
		case <-time.After(cacheLockTimeout):
		}
		// The leader has finished (or we timed out). Recompute our variant key:
		// the leader may have just learned this primary's Vary, so our key now
		// matches the stored entry IF our varied-header values match the leader's
		// (otherwise we correctly miss and fetch our own variant — never serving a
		// wrong Vary variant). A miss here falls through to our own origin fetch.
		if c.tryServeHit(w, r, c.variantHash(primaryHex, r)) {
			return
		}
		w.Header().Set("X-Cache", "MISS")
		next.ServeHTTP(w, r)
		return
	}

	defer c.release(variantHex, lock)
	tw := &teeWriter{rw: w, r: r, c: c, method: r.Method, primaryHex: primaryHex}
	defer tw.cleanup() // panic-safe: abort an uncommitted entry if finish never ran
	next.ServeHTTP(tw, r)
	tw.finish()
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
// normalization. scheme reflects the terminating listener via X-Forwarded-Proto
// (set by the parapet server), defaulting to the request TLS state.
func (c *Cache) primaryHash(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h // drop :port (SplitHostPort errors when there is none)
	}
	uri := r.URL.RequestURI()
	sum := sha256.Sum256([]byte(host + "\n" + r.Method + "\n" + scheme + "\n" + uri))
	return hex.EncodeToString(sum[:16])
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
	// Bound the map: a dropped entry just re-learns on the next fill.
	if len(c.primaryVary) >= maxPrimaryVary {
		if _, exists := c.primaryVary[primaryHex]; !exists {
			c.primaryVary = make(map[string][]string, maxPrimaryVary)
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
	h.Set("X-Cache", tag)
	w.WriteHeader(m.Status)
	if r.Method == http.MethodHead || m.Status == http.StatusNoContent {
		return
	}
	_, _ = w.Write(body)
}
