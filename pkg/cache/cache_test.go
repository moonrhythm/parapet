package cache

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type originSpec struct {
	body   []byte
	header http.Header
	status int
	sleep  time.Duration
}

func origin(spec originSpec, calls *int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if spec.sleep > 0 {
			time.Sleep(spec.sleep)
		}
		for k, vs := range spec.header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		if w.Header().Get("Content-Length") == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(spec.body)))
		}
		st := spec.status
		if st == 0 {
			st = http.StatusOK
		}
		w.WriteHeader(st)
		_, _ = w.Write(spec.body)
	})
}

// storePut writes an entry directly to a Storage backend (Writer + Commit), for
// tests that seed or rewrite stored state.
func storePut(t *testing.T, s Storage, key string, m Meta, body []byte) {
	t.Helper()
	w, err := s.Writer(key)
	require.NoError(t, err)
	if len(body) > 0 {
		_, err = w.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, w.Commit(m))
}

func do(c *Cache, h http.Handler, method, target string, reqHeader http.Header) *httptest.ResponseRecorder {
	mw := c.ServeHandler(h)
	req := httptest.NewRequest(method, target, nil)
	for k, vs := range reqHeader {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	return rec
}

// eachBackend runs fn against both storage backends so the middleware behavior is
// verified identically for memory and disk.
//
// LockTimeout is pinned far above any real fill duration so the single-flight
// collapse tests can't flake when a slow CI leader (origin sleep + disk fsyncs
// under -race) stretches a fill past the 2s production default and pushes
// followers into self-fetch. Timeout behavior itself is covered by
// TestCache_LockTimeoutConfigurable, which builds its own cache.
func eachBackend(t *testing.T, fn func(t *testing.T, c *Cache)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		fn(t, New(NewMemory(1<<20), Options{MaxFileSize: 1024, LockTimeout: 30 * time.Second}))
	})
	t.Run("disk", func(t *testing.T) {
		d, err := NewDisk(t.TempDir(), 1<<20)
		require.NoError(t, err)
		fn(t, New(d, Options{MaxFileSize: 1024, LockTimeout: 30 * time.Second}))
	})
}

func TestCache_MissThenHit(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("hello"), header: hdr("Cache-Control", "max-age=60")}, &calls)
		r1 := do(c, h, "GET", "http://acme.com/a", nil)
		assert.Equal(t, "MISS", r1.Header().Get("X-Cache"))
		assert.Equal(t, "hello", r1.Body.String())
		r2 := do(c, h, "GET", "http://acme.com/a", nil)
		assert.Equal(t, "HIT", r2.Header().Get("X-Cache"))
		assert.Equal(t, "hello", r2.Body.String())
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls), "origin contacted once")
	})
}

func TestCache_NonCacheableAlwaysMiss(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("dyn")}, &calls) // no freshness
		do(c, h, "GET", "http://acme.com/d", nil)
		r := do(c, h, "GET", "http://acme.com/d", nil)
		assert.Equal(t, "MISS", r.Header().Get("X-Cache"))
		assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
	})
}

func TestCache_IgnoresClientCacheControl(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("x"), header: hdr("Cache-Control", "max-age=60")}, &calls)
		do(c, h, "GET", "http://acme.com/a", nil)
		r := do(c, h, "GET", "http://acme.com/a", hdr("Cache-Control", "no-cache"))
		assert.Equal(t, "HIT", r.Header().Get("X-Cache"))
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls))
	})
}

func TestCache_VarySeparatesVariants(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("v"), header: hdr("Cache-Control", "max-age=60", "Vary", "Accept-Encoding")}, &calls)
		do(c, h, "GET", "http://acme.com/a", hdr("Accept-Encoding", "gzip"))
		gz := do(c, h, "GET", "http://acme.com/a", hdr("Accept-Encoding", "gzip"))
		assert.Equal(t, "HIT", gz.Header().Get("X-Cache"))
		br := do(c, h, "GET", "http://acme.com/a", hdr("Accept-Encoding", "br"))
		assert.Equal(t, "MISS", br.Header().Get("X-Cache"))
		assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
	})
}

func TestCache_PerObjectCapNotCached(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: make([]byte, 2000), header: hdr("Cache-Control", "max-age=60")}, &calls)
		do(c, h, "GET", "http://acme.com/big", nil)
		r := do(c, h, "GET", "http://acme.com/big", nil)
		assert.Equal(t, "MISS", r.Header().Get("X-Cache"))
		assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
	})
}

func TestCache_PostNotCached(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("body"), header: hdr("Cache-Control", "max-age=60")}, &calls)
		r := do(c, h, "POST", "http://acme.com/a", nil)
		assert.Equal(t, "", r.Header().Get("X-Cache"))
	})
}

func TestCache_FarFutureFreshnessStillHits(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("immortal"), header: hdr("Cache-Control", "max-age=7445000000")}, &calls)
		assert.Equal(t, "MISS", do(c, h, "GET", "http://acme.com/far", nil).Header().Get("X-Cache"))
		assert.Equal(t, "HIT", do(c, h, "GET", "http://acme.com/far", nil).Header().Get("X-Cache"))
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls))
	})
}

func TestCache_SingleFlightCollapsesConcurrentMisses(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("slow"), header: hdr("Cache-Control", "max-age=60"), sleep: 60 * time.Millisecond}, &calls)
		mw := c.ServeHandler(h)
		const n = 12
		var wg, start sync.WaitGroup
		var miss, hit int32
		start.Add(1)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start.Wait()
				rec := httptest.NewRecorder()
				mw.ServeHTTP(rec, httptest.NewRequest("GET", "http://acme.com/s", nil))
				assert.Equal(t, "slow", rec.Body.String())
				switch rec.Header().Get("X-Cache") {
				case "MISS":
					atomic.AddInt32(&miss, 1)
				case "HIT":
					atomic.AddInt32(&hit, 1)
				}
			}()
		}
		start.Done()
		wg.Wait()
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls), "concurrent misses collapse to one origin fetch")
		assert.EqualValues(t, 1, atomic.LoadInt32(&miss))
		assert.EqualValues(t, n-1, atomic.LoadInt32(&hit))
	})
}

func TestCache_SingleFlightVaryFirstFill(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("vb"), header: hdr("Cache-Control", "max-age=60", "Vary", "Accept-Encoding"), sleep: 60 * time.Millisecond}, &calls)
		mw := c.ServeHandler(h)
		const n = 12
		var wg, start sync.WaitGroup
		start.Add(1)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start.Wait()
				req := httptest.NewRequest("GET", "http://acme.com/v", nil)
				req.Header.Set("Accept-Encoding", "gzip")
				rec := httptest.NewRecorder()
				mw.ServeHTTP(rec, req)
				assert.Equal(t, "vb", rec.Body.String())
			}()
		}
		start.Done()
		wg.Wait()
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls), "concurrent first-fill of a Vary'd URL collapses to one fetch")
	})
}

// A cold-key stampede spanning multiple Vary variants collapses to one origin
// fetch PER VARIANT (not one per follower), and every variant is cached. Each
// requester still receives its own variant's body.
func TestCache_SingleFlightCollapsesCrossVariant(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			enc := r.Header.Get("Accept-Encoding")
			time.Sleep(60 * time.Millisecond) // hold the lock so a stampede forms
			body := []byte("body-" + enc)
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("Vary", "Accept-Encoding")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(200)
			_, _ = w.Write(body)
		})
		mw := c.ServeHandler(h)

		var wg, start sync.WaitGroup
		start.Add(1)
		for _, enc := range []string{"gzip", "br", "br", "br", "br", "br"} {
			wg.Add(1)
			go func(enc string) {
				defer wg.Done()
				start.Wait()
				req := httptest.NewRequest("GET", "http://acme.com/v", nil)
				req.Header.Set("Accept-Encoding", enc)
				rec := httptest.NewRecorder()
				mw.ServeHTTP(rec, req)
				assert.Equal(t, "body-"+enc, rec.Body.String(), "each variant gets its own body")
			}(enc)
		}
		start.Done()
		wg.Wait()

		// Two distinct variants (gzip, br) => exactly two origin fetches, regardless
		// of how many followers each had.
		assert.EqualValues(t, 2, atomic.LoadInt32(&calls), "one fetch per variant, not per follower")
		// Both variants ended up cached.
		assert.Equal(t, "HIT", do(c, h, "GET", "http://acme.com/v", hdr("Accept-Encoding", "gzip")).Header().Get("X-Cache"))
		assert.Equal(t, "HIT", do(c, h, "GET", "http://acme.com/v", hdr("Accept-Encoding", "br")).Header().Get("X-Cache"))
	})
}

// A short Options.LockTimeout makes followers give up on a slow leader and fetch
// the origin themselves rather than wait (the default 2s would collapse them).
//
// The origin is gated on channels rather than a 200ms sleep: a sleeping leader
// raced the followers' arrival (a stalled follower set could wake after the
// leader's commit and HIT, leaving calls==1). Holding the leader inside the
// origin until the followers finish makes the timeout path deterministic.
func TestCache_LockTimeoutConfigurable(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024, LockTimeout: 20 * time.Millisecond})
	var calls int32
	originEntered := make(chan struct{}) // closed when the leader is inside the origin
	originGate := make(chan struct{})    // leader blocks here until followers finish
	var leaderOnce sync.Once
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		isLeader := false
		leaderOnce.Do(func() { isLeader = true; close(originEntered) })
		if isLeader {
			<-originGate // hold the fill lock for the whole follower phase
		}
		body := []byte("slow")
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(200)
		_, _ = w.Write(body)
	})
	mw := c.ServeHandler(h)

	// Leader first: provably holds the fill lock before any follower starts.
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, httptest.NewRequest("GET", "http://acme.com/s", nil))
		assert.Equal(t, "slow", rec.Body.String())
	}()
	<-originEntered

	// Followers: the leader cannot commit, so each MUST take the timeout path and
	// fetch the (ungated for them) origin itself.
	startT := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, httptest.NewRequest("GET", "http://acme.com/s", nil))
			assert.Equal(t, "slow", rec.Body.String())
		}()
	}
	wg.Wait()
	elapsed := time.Since(startT)
	close(originGate)
	<-leaderDone

	// 1 leader + 3 followers, deterministically.
	assert.EqualValues(t, 4, atomic.LoadInt32(&calls), "short LockTimeout: every follower fetched the origin instead of waiting")
	// Sensitivity guard: if LockTimeout were ignored, all three followers would wait
	// the full 2s default (concurrently) before self-fetching, so ANY bound < 2s
	// detects the regression. 1.5s keeps a 0.5s guard band while tolerating ~1.5s of
	// scheduler stall over a nominal ~22ms phase — the bound itself must not become
	// the flake.
	assert.Less(t, elapsed, 1500*time.Millisecond, "followers honored the configured 20ms LockTimeout, not the 2s default")
}

func TestCache_ExpiredEntryIsMiss(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("e"), header: hdr("Cache-Control", "max-age=60")}, &calls)
		req := httptest.NewRequest("GET", "http://acme.com/exp", nil)
		do(c, h, "GET", "http://acme.com/exp", nil) // fill
		// Force the stored entry stale by rewriting its meta with a past FreshUntil.
		variant := c.variantHash(c.primaryHash(req), req)
		m, body, ok := c.storage.Get(variant)
		require.True(t, ok)
		m.FreshUntil = time.Now().Add(-time.Minute).UnixNano()
		storePut(t, c.storage, variant, m, body)
		r := do(c, h, "GET", "http://acme.com/exp", nil)
		assert.Equal(t, "MISS", r.Header().Get("X-Cache"), "expired entry is a miss")
		assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
	})
}

func TestCache_InvalidatedAfterPurgesHit(t *testing.T) {
	backends := []struct {
		name string
		new  func() Storage
	}{
		{"memory", func() Storage { return NewMemory(1 << 20) }},
		{"disk", func() Storage {
			d, err := NewDisk(t.TempDir(), 1<<20)
			require.NoError(t, err)
			return d
		}},
	}
	for _, bk := range backends {
		t.Run(bk.name, func(t *testing.T) {
			var epoch atomic.Int64 // invalidation epoch (unix nanos); 0 = nothing purged
			c := New(bk.new(), Options{
				MaxFileSize:      1024,
				InvalidatedAfter: func(_ *http.Request, _ Meta) int64 { return epoch.Load() },
			})
			var calls int32
			h := origin(originSpec{body: []byte("p"), header: hdr("Cache-Control", "max-age=60")}, &calls)

			// epoch 0: the hook is present but purges nothing (Created > 0), so the
			// entry fills then hits — the no-purge fast path stays a hit.
			assert.Equal(t, "MISS", do(c, h, "GET", "http://acme.com/p", nil).Header().Get("X-Cache"))
			assert.Equal(t, "HIT", do(c, h, "GET", "http://acme.com/p", nil).Header().Get("X-Cache"))
			assert.EqualValues(t, 1, atomic.LoadInt32(&calls))

			// Purge: bump the epoch to now. The cached entry was created before this,
			// so its next lookup is reaped and served as a miss (re-fetch).
			epoch.Store(time.Now().UnixNano())
			r := do(c, h, "GET", "http://acme.com/p", nil)
			assert.Equal(t, "MISS", r.Header().Get("X-Cache"), "entry created at/before the epoch is purged")
			assert.EqualValues(t, 2, atomic.LoadInt32(&calls))

			// The re-fill was created after the epoch, so it hits again (a purge
			// invalidates only what existed when it was issued).
			assert.Equal(t, "HIT", do(c, h, "GET", "http://acme.com/p", nil).Header().Get("X-Cache"))
			assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
		})
	}
}

func TestStorage_RangeVisitsEntriesAndAllowsDelete(t *testing.T) {
	backends := []struct {
		name string
		new  func() Storage
	}{
		{"memory", func() Storage { return NewMemory(1 << 20) }},
		{"disk", func() Storage {
			d, err := NewDisk(t.TempDir(), 1<<20)
			require.NoError(t, err)
			return d
		}},
	}
	for _, bk := range backends {
		t.Run(bk.name, func(t *testing.T) {
			s := bk.new()
			fresh := time.Now().Add(time.Hour).UnixNano()
			for _, k := range []string{"aa01", "bb02", "cc03"} {
				storePut(t, s, k, Meta{PrimaryHex: k, Host: "acme.com", URI: "/" + k, Created: 1, FreshUntil: fresh, Size: 3}, []byte("xyz"))
			}

			// Range sees all three and exposes Meta; delete one from inside fn.
			seen := map[string]Meta{}
			s.Range(func(key string, m Meta) bool {
				seen[key] = m
				if key == "bb02" {
					s.Delete(key)
				}
				return true
			})
			assert.Len(t, seen, 3)
			assert.Equal(t, "acme.com", seen["aa01"].Host)
			assert.Equal(t, "/aa01", seen["aa01"].URI)

			_, _, ok := s.Get("bb02")
			assert.False(t, ok, "deleted from within Range")
			cnt := 0
			s.Range(func(string, Meta) bool { cnt++; return true })
			assert.Equal(t, 2, cnt)

			// Early stop: returning false halts iteration.
			visited := 0
			s.Range(func(string, Meta) bool { visited++; return false })
			assert.Equal(t, 1, visited)
		})
	}
}

func TestCache_MetaCarriesHostURIForRange(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("x"), header: hdr("Cache-Control", "max-age=60")}, &calls)
		do(c, h, "GET", "http://Acme.COM:8443/p?q=1", nil) // fill (mixed case + port)

		got := map[string]Meta{}
		c.storage.Range(func(k string, m Meta) bool { got[k] = m; return true })
		require.Len(t, got, 1)
		for _, m := range got {
			assert.Equal(t, "acme.com", m.Host, "host normalized (lowercased, port-stripped) for Range maintenance")
			assert.Equal(t, "/p?q=1", m.URI)
		}
	})
}

func TestCache_MetaCapturesCacheTags(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		// Comma-separated, with surrounding spaces and a duplicate.
		h := origin(originSpec{body: []byte("x"), header: hdr("Cache-Control", "max-age=60", "Cache-Tag", "product-42, category-shoes , product-42")}, &calls)
		do(c, h, "GET", "http://acme.com/p", nil) // fill

		got := map[string]Meta{}
		c.storage.Range(func(k string, m Meta) bool { got[k] = m; return true })
		require.Len(t, got, 1)
		for _, m := range got {
			assert.Equal(t, []string{"product-42", "category-shoes"}, m.Tags, "trimmed + de-duplicated, order preserved")
		}
	})
}

func TestParseCacheTags(t *testing.T) {
	assert.Nil(t, parseCacheTags(http.Header{}), "no header -> nil")
	assert.Nil(t, parseCacheTags(hdr("Cache-Tag", " , ,")), "only blanks -> nil")
	assert.Equal(t, []string{"a", "b", "c"}, parseCacheTags(hdr("Cache-Tag", "a,b,c")))
	// Multiple header lines are merged.
	multi := http.Header{"Cache-Tag": {"a, b", "b, c"}}
	assert.Equal(t, []string{"a", "b", "c"}, parseCacheTags(multi), "merged across header lines, deduped")
	// Over-long tags are dropped; the count is capped.
	long := strings.Repeat("x", maxCacheTagLen+1)
	assert.Equal(t, []string{"ok"}, parseCacheTags(hdr("Cache-Tag", long+", ok")), "over-length tag dropped")
	var many []string
	for i := 0; i < maxCacheTags+10; i++ {
		many = append(many, "t"+strconv.Itoa(i))
	}
	assert.Len(t, parseCacheTags(hdr("Cache-Tag", strings.Join(many, ","))), maxCacheTags, "count capped")
}

func TestCache_PanicDuringFillIsSafe(t *testing.T) {
	// Buffering (no temp file) makes a fill panic-safe: nothing is persisted, and
	// the cache stays usable afterward.
	eachBackend(t, func(t *testing.T, c *Cache) {
		panicky := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("partial"))
			panic("boom")
		})
		mw := c.ServeHandler(panicky)
		func() {
			defer func() { _ = recover() }()
			mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://acme.com/boom", nil))
		}()
		var calls int32
		ok := origin(originSpec{body: []byte("ok"), header: hdr("Cache-Control", "max-age=60")}, &calls)
		assert.Equal(t, "MISS", do(c, ok, "GET", "http://acme.com/after", nil).Header().Get("X-Cache"))
	})
}

// A response to an Authorization-bearing request must not be served to other
// users from a shared cache unless the origin explicitly opted in (RFC 9111 §3.5).
func TestCache_AuthorizationNotSharedAcrossUsers(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		// Bare max-age (no public/s-maxage/must-revalidate) on an authenticated request.
		h := origin(originSpec{body: []byte("user-A-private"), header: hdr("Cache-Control", "max-age=60")}, &calls)

		// User A (authenticated): not shared-cacheable, so nothing is stored.
		rA := do(c, h, "GET", "http://acme.com/account", hdr("Authorization", "Bearer tokenA"))
		assert.Equal(t, "MISS", rA.Header().Get("X-Cache"))

		// User B (no credentials) must not receive A's body from the cache.
		rB := do(c, h, "GET", "http://acme.com/account", nil)
		assert.Equal(t, "MISS", rB.Header().Get("X-Cache"), "authorized response must not be shared with other users")
		assert.EqualValues(t, 2, atomic.LoadInt32(&calls), "each request reaches the origin; A's response is never shared")
	})
}

// public opt-in makes an authorized response shared-cacheable (RFC 9111 §3.5).
func TestCache_AuthorizationCachedWithExplicitSharedOptIn(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("shared"), header: hdr("Cache-Control", "public, max-age=60")}, &calls)
		assert.Equal(t, "MISS", do(c, h, "GET", "http://acme.com/pub", hdr("Authorization", "Bearer t")).Header().Get("X-Cache"))
		assert.Equal(t, "HIT", do(c, h, "GET", "http://acme.com/pub", nil).Header().Get("X-Cache"))
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls))
	})
}

// The stored Meta must NOT embed the X-Cache tag: sanitizeHeader snapshots the
// response headers BEFORE WriteHeader sets X-Cache. A reorder would leak
// "X-Cache: MISS" into every stored entry (surfacing via Range and any serve path
// that doesn't re-Set it). Range is the only way to observe the raw stored header.
func TestCache_StoredMetaExcludesXCache(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("x"), header: hdr("Cache-Control", "max-age=60")}, &calls)
		require.Equal(t, "MISS", do(c, h, "GET", "http://acme.com/p", nil).Header().Get("X-Cache")) // fill

		n := 0
		c.storage.Range(func(_ string, m Meta) bool {
			n++
			assert.Equal(t, "", m.Header.Get("X-Cache"), "stored Meta must not embed the X-Cache tag")
			return true
		})
		require.Equal(t, 1, n)
	})
}

// Hop-by-hop headers must not be stored in, or served from, the shared cache;
// end-to-end headers must survive.
func TestCache_HopByHopHeadersStripped(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{
			body:   []byte("x"),
			header: hdr("Cache-Control", "max-age=60", "Connection", "keep-alive", "Keep-Alive", "timeout=5", "X-App", "yes"),
		}, &calls)
		require.Equal(t, "MISS", do(c, h, "GET", "http://acme.com/hbh", nil).Header().Get("X-Cache")) // fill

		r := do(c, h, "GET", "http://acme.com/hbh", nil) // HIT, served from cache
		require.Equal(t, "HIT", r.Header().Get("X-Cache"))
		assert.Equal(t, "", r.Header().Get("Connection"), "hop-by-hop Connection must not be served from cache")
		assert.Equal(t, "", r.Header().Get("Keep-Alive"), "hop-by-hop Keep-Alive must not be served from cache")
		assert.Equal(t, "yes", r.Header().Get("X-App"), "end-to-end header preserved")

		c.storage.Range(func(_ string, m Meta) bool {
			assert.Equal(t, "", m.Header.Get("Connection"), "hop-by-hop must not be stored")
			assert.Equal(t, "yes", m.Header.Get("X-App"))
			return true
		})
	})
}

// A body that exceeds MaxFileSize only mid-stream (Content-Length is within the
// cap, but the origin writes more) is aborted: the full body is still served, but
// nothing is cached.
func TestCache_OversizeMidStreamNotCached(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		body := make([]byte, 2000) // exceeds eachBackend's MaxFileSize (1024)
		h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("Content-Length", "100") // within the cap; we write more
			w.WriteHeader(200)
			_, _ = w.Write(body)
		})
		r1 := do(c, h, "GET", "http://acme.com/over", nil)
		assert.Equal(t, "MISS", r1.Header().Get("X-Cache"))
		assert.Len(t, r1.Body.Bytes(), 2000, "full body still served despite not caching")

		r2 := do(c, h, "GET", "http://acme.com/over", nil)
		assert.Equal(t, "MISS", r2.Header().Get("X-Cache"), "oversize response is not cached")
		assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
	})
}

// HEAD responses are cached and served with their headers but no body.
func TestCache_HeadCachedAndServed(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("Content-Length", "42") // reflects the GET length; no body for HEAD
			w.Header().Set("X-App", "head")
			w.WriteHeader(200)
		})
		assert.Equal(t, "MISS", do(c, h, "HEAD", "http://acme.com/h", nil).Header().Get("X-Cache"))
		r := do(c, h, "HEAD", "http://acme.com/h", nil)
		assert.Equal(t, "HIT", r.Header().Get("X-Cache"))
		assert.Equal(t, "head", r.Header().Get("X-App"))
		assert.Empty(t, r.Body.Bytes(), "HEAD hit serves no body")
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls), "HEAD cached after the first fetch")
	})
}

// A 204 No Content (bodiless) response is cached and served with no body.
func TestCache_NoContent204Cached(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{status: http.StatusNoContent, header: hdr("Cache-Control", "max-age=60")}, &calls)
		assert.Equal(t, "MISS", do(c, h, "GET", "http://acme.com/nc", nil).Header().Get("X-Cache"))
		r := do(c, h, "GET", "http://acme.com/nc", nil)
		assert.Equal(t, "HIT", r.Header().Get("X-Cache"))
		assert.Equal(t, http.StatusNoContent, r.Code)
		assert.Empty(t, r.Body.Bytes(), "204 hit serves no body")
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls))
	})
}

func TestCache_PrimaryVaryBoundedEviction(t *testing.T) {
	defer func(orig int) { maxPrimaryVary = orig }(maxPrimaryVary)
	maxPrimaryVary = 3
	c := New(NewMemory(1<<20), Options{})
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		c.setPrimaryVary(k, []string{"accept-encoding"})
	}
	c.pvMu.RLock()
	n := len(c.primaryVary)
	_, hasE := c.primaryVary["e"]
	c.pvMu.RUnlock()
	assert.Equal(t, 3, n, "map stays at the cap (one evicted), not wiped")
	assert.True(t, hasE, "the most recent entry is retained")
}

func TestCache_ObjectLargerThanCapNotCached(t *testing.T) {
	c := New(NewMemory(500), Options{MaxFileSize: 1 << 20}) // per-object cap > storage cap
	var calls int32
	h := origin(originSpec{body: make([]byte, 800), header: hdr("Cache-Control", "max-age=60")}, &calls)
	assert.Equal(t, "MISS", do(c, h, "GET", "http://acme.com/big", nil).Header().Get("X-Cache"))
	assert.Equal(t, "MISS", do(c, h, "GET", "http://acme.com/big", nil).Header().Get("X-Cache"), "object larger than the storage cap is not cached")
	assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
}

func TestCache_HitCarriesAgeHeader(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		req := httptest.NewRequest("GET", "http://acme.com/age", nil)
		primary := c.primaryHash(req)
		key := c.variantHash(primary, req)
		// Anchor both seeded timestamps to one instant so the upper Age bound can
		// be derived from measured elapsed time — a fixed `age <= 31` raced the
		// real clock against storePut's disk fsyncs plus the request cycle.
		seedAt := time.Now()
		storePut(t, c.storage, key, Meta{
			Status: 200, Header: http.Header{}, PrimaryHex: primary,
			Created:    seedAt.Add(-30 * time.Second).UnixNano(),
			FreshUntil: seedAt.Add(time.Hour).UnixNano(), Size: 3,
		}, []byte("abc"))

		r := do(c, http.NotFoundHandler(), "GET", "http://acme.com/age", nil)
		require.Equal(t, "HIT", r.Header().Get("X-Cache"))
		age, err := strconv.Atoi(r.Header().Get("Age"))
		require.NoError(t, err)
		assert.GreaterOrEqual(t, age, 30)
		// Measured elapsed >= serve-time elapsed, so age = 30+floor(e_serve) is
		// always <= 30+floor(measured)+1; in a fast run this bound stays ~31.
		assert.LessOrEqual(t, age, 30+int(time.Since(seedAt).Seconds())+1)
	})
}

func TestCache_XFPCanonicalizedInKey(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024})
	mk := func(xfp string) string {
		r := httptest.NewRequest("GET", "http://acme.com/p", nil)
		if xfp != "" {
			r.Header.Set("X-Forwarded-Proto", xfp)
		}
		return c.primaryHash(r)
	}
	assert.Equal(t, mk("https"), mk("HTTPS"), "case-insensitive https collapses")
	assert.Equal(t, mk(""), mk("evil-1"), "junk XFP falls back to TLS state, like no XFP")
	assert.Equal(t, mk("evil-1"), mk("evil-2"), "distinct junk values don't fragment the key")
	assert.NotEqual(t, mk("http"), mk("https"), "legit http vs https still differ")
}

func TestCache_CacheablePredicate(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024, Cacheable: func(r *http.Request) bool {
		return r.URL.Path != "/no"
	}})
	var calls int32
	h := origin(originSpec{body: []byte("x"), header: hdr("Cache-Control", "max-age=60")}, &calls)
	assert.Equal(t, "", do(c, h, "GET", "http://acme.com/no", nil).Header().Get("X-Cache"))
	assert.Equal(t, "", do(c, h, "GET", "http://acme.com/no", nil).Header().Get("X-Cache"))
	assert.Equal(t, "MISS", do(c, h, "GET", "http://acme.com/yes", nil).Header().Get("X-Cache"))
	assert.Equal(t, "HIT", do(c, h, "GET", "http://acme.com/yes", nil).Header().Get("X-Cache"))
}

func TestCache_RangeRequestBypassesCache(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("0123456789"), header: hdr("Cache-Control", "max-age=60")}, &calls)
		assert.Equal(t, "MISS", do(c, h, "GET", "http://acme.com/r", nil).Header().Get("X-Cache"))
		assert.Equal(t, "HIT", do(c, h, "GET", "http://acme.com/r", nil).Header().Get("X-Cache"))
		r := do(c, h, "GET", "http://acme.com/r", hdr("Range", "bytes=0-3"))
		assert.Equal(t, "", r.Header().Get("X-Cache"), "Range request bypasses the cache")
		assert.EqualValues(t, 2, atomic.LoadInt32(&calls), "Range request reaches the origin")
	})
}

// eachBackendDecoupled runs fn against both backends with DecoupleFill enabled.
// LockTimeout is pinned high for the same reason as eachBackend: a contended fill
// (LeaderHeadersSanitized) must never push followers into the 2s-default timeout
// self-fetch path when CI stalls the leader's commit.
func eachBackendDecoupled(t *testing.T, fn func(t *testing.T, c *Cache)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		fn(t, New(NewMemory(1<<20), Options{MaxFileSize: 1024, DecoupleFill: true, LockTimeout: 30 * time.Second}))
	})
	t.Run("disk", func(t *testing.T) {
		d, err := NewDisk(t.TempDir(), 1<<20)
		require.NoError(t, err)
		fn(t, New(d, Options{MaxFileSize: 1024, DecoupleFill: true, LockTimeout: 30 * time.Second}))
	})
}

// blockingRW is a ResponseWriter whose first body Write blocks until release is
// closed (simulating a slow client). It signals entry to that Write by closing
// written. Header/WriteHeader never block.
type blockingRW struct {
	hdr     http.Header
	release chan struct{}
	written chan struct{}
	body    bytes.Buffer
	status  int
	once    sync.Once
}

func (b *blockingRW) Header() http.Header {
	if b.hdr == nil {
		b.hdr = http.Header{}
	}
	return b.hdr
}
func (b *blockingRW) WriteHeader(code int) { b.status = code }
func (b *blockingRW) Write(p []byte) (int, error) {
	b.once.Do(func() { close(b.written) })
	<-b.release
	return b.body.Write(p)
}

// failingWriterStorage wraps a Storage so its EntryWriter.Write always errors,
// simulating a mid-fill storage failure.
type failingWriterStorage struct{ Storage }

func (s failingWriterStorage) Writer(key string) (EntryWriter, error) {
	w, err := s.Storage.Writer(key)
	if err != nil {
		return nil, err
	}
	return failingWriter{w}, nil
}

type failingWriter struct{ EntryWriter }

func (failingWriter) Write([]byte) (int, error) { return 0, assert.AnError }

func TestCache_DecoupleFill_MissThenHit(t *testing.T) {
	eachBackendDecoupled(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := origin(originSpec{body: []byte("hello"), header: hdr("Cache-Control", "max-age=60", "X-App", "v")}, &calls)
		r1 := do(c, h, "GET", "http://acme.com/a", nil)
		assert.Equal(t, "MISS", r1.Header().Get("X-Cache"), "leader served from the committed entry")
		assert.Equal(t, "hello", r1.Body.String())
		assert.Equal(t, "v", r1.Header().Get("X-App"), "leader gets the cached response headers")
		r2 := do(c, h, "GET", "http://acme.com/a", nil)
		assert.Equal(t, "HIT", r2.Header().Get("X-Cache"))
		assert.Equal(t, "hello", r2.Body.String())
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls), "origin contacted once")
	})
}

// The headline property: under contention, a leader whose own client is blocked
// must NOT hold the fill lock — followers hit the just-committed entry immediately.
func TestCache_DecoupleFill_SlowLeaderDoesNotBlockFollowers(t *testing.T) {
	// LockTimeout is pinned high so a CI stall between the followers registering
	// and the leader committing can't push them into the timeout/self-fetch path.
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1 << 20, DecoupleFill: true, LockTimeout: 30 * time.Second})
	originEntered := make(chan struct{})
	originGate := make(chan struct{})
	var calls int32
	var enteredOnce sync.Once
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		// Once-guarded so a single-flight regression fails the calls assert below
		// instead of panicking on a double close.
		enteredOnce.Do(func() { close(originEntered) }) // the leader is in origin
		<-originGate                                    // hold the leader here so followers can pile onto the lock
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Content-Length", "7")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("payload"))
	})
	mw := c.ServeHandler(h)

	// Leader with a slow client.
	leaderRW := &blockingRW{release: make(chan struct{}), written: make(chan struct{})}
	leaderDone := make(chan struct{})
	go func() {
		mw.ServeHTTP(leaderRW, httptest.NewRequest("GET", "http://acme.com/x", nil))
		close(leaderDone)
	}()
	<-originEntered // leader holds the lock, blocked in origin

	// Followers pile onto the lock while the leader is still in origin (contention).
	var fwg sync.WaitGroup
	recs := make([]*httptest.ResponseRecorder, 5)
	for i := range recs {
		recs[i] = httptest.NewRecorder()
		fwg.Add(1)
		go func(i int) {
			defer fwg.Done()
			mw.ServeHTTP(recs[i], httptest.NewRequest("GET", "http://acme.com/x", nil))
		}(i)
	}
	// White-box wait: a fixed sleep here raced follower scheduling — if none had
	// registered yet, the leader's WriteHeader saw waiters==0 and fell back to
	// lockstep (wedging on the blocked client while holding the fill lock).
	// Waiting for waiters==5 proves every follower passed its storage lookup AND
	// acquire(), so none can wake later as a second leader and re-run the origin.
	require.Eventually(t, func() bool {
		c.lockMu.Lock()
		defer c.lockMu.Unlock()
		for _, l := range c.locks {
			if l.waiters.Load() == int32(len(recs)) {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond, "all followers registered on the fill lock")

	close(originGate) // leader proceeds; WriteHeader sees waiters>0 -> decouple, commit, release
	fwg.Wait()        // followers complete WITHOUT the leader's blocked client being released

	for _, rec := range recs {
		assert.Equal(t, "HIT", rec.Header().Get("X-Cache"), "follower hits while the leader's client is blocked")
		assert.Equal(t, "payload", rec.Body.String())
	}
	assert.EqualValues(t, 1, atomic.LoadInt32(&calls), "origin contacted once despite the slow leader client")

	close(leaderRW.release)
	<-leaderDone
	assert.Equal(t, "payload", leaderRW.body.String())
	assert.Equal(t, "MISS", leaderRW.hdr.Get("X-Cache"))
}

func TestCache_DecoupleFill_HeadAndNoContent(t *testing.T) {
	eachBackendDecoupled(t, func(t *testing.T, c *Cache) {
		var hcalls int32
		hh := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&hcalls, 1)
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("Content-Length", "5")
			w.WriteHeader(200)
		})
		assert.Equal(t, "MISS", do(c, hh, "HEAD", "http://acme.com/h", nil).Header().Get("X-Cache"))
		rh := do(c, hh, "HEAD", "http://acme.com/h", nil)
		assert.Equal(t, "HIT", rh.Header().Get("X-Cache"))
		assert.Empty(t, rh.Body.Bytes(), "HEAD serves no body")
		assert.EqualValues(t, 1, atomic.LoadInt32(&hcalls))

		var ncalls int32
		h204 := origin(originSpec{status: http.StatusNoContent, header: hdr("Cache-Control", "max-age=60")}, &ncalls)
		assert.Equal(t, "MISS", do(c, h204, "GET", "http://acme.com/n", nil).Header().Get("X-Cache"))
		r2 := do(c, h204, "GET", "http://acme.com/n", nil)
		assert.Equal(t, "HIT", r2.Header().Get("X-Cache"))
		assert.Equal(t, http.StatusNoContent, r2.Code)
		assert.Empty(t, r2.Body.Bytes())
	})
}

// A non-cacheable response is unaffected by DecoupleFill: it still streams to the
// client (in lockstep).
func TestCache_DecoupleFill_NonCacheableStreams(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024, DecoupleFill: true})
	var calls int32
	h := origin(originSpec{body: []byte("dynamic")}, &calls) // no freshness
	r := do(c, h, "GET", "http://acme.com/d", nil)
	assert.Equal(t, "MISS", r.Header().Get("X-Cache"))
	assert.Equal(t, "dynamic", r.Body.String(), "non-cacheable response streams to the client")
}

// A truncated cacheable response (origin under-writes Content-Length) is not cached;
// the decoupled leader is served the headers with no body (a clean failure rather
// than a torn partial).
func TestCache_DecoupleFill_TruncatedNotCached(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1 << 20, DecoupleFill: true})
	var calls int32
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Content-Length", "100") // claims 100...
		w.WriteHeader(200)
		_, _ = w.Write([]byte("short")) // ...writes 5
	})
	r1 := do(c, h, "GET", "http://acme.com/t", nil)
	assert.Equal(t, "MISS", r1.Header().Get("X-Cache"))
	assert.Equal(t, "short", r1.Body.String(), "leader gets the bytes the origin actually wrote (as in lockstep)")
	r2 := do(c, h, "GET", "http://acme.com/t", nil)
	assert.Equal(t, "MISS", r2.Header().Get("X-Cache"), "truncated response is not cached")
	assert.EqualValues(t, 2, atomic.LoadInt32(&calls))
}

// decoupledContended drives one decoupled leader fill that is guaranteed contended
// (followers wait on the lock before the leader's WriteHeader, via a gated origin),
// and returns the leader's recorder plus the origin call count. write produces the
// response inside the origin (after the gate).
func decoupledContended(t *testing.T, c *Cache, target string, write func(w http.ResponseWriter)) (*httptest.ResponseRecorder, int32) {
	t.Helper()
	originEntered := make(chan struct{})
	originGate := make(chan struct{})
	var calls int32
	var once sync.Once
	mw := c.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		once.Do(func() { close(originEntered) })
		<-originGate
		write(w)
	}))

	leaderRec := httptest.NewRecorder()
	leaderDone := make(chan struct{})
	go func() {
		mw.ServeHTTP(leaderRec, httptest.NewRequest("GET", target, nil))
		close(leaderDone)
	}()
	<-originEntered // leader holds the lock, blocked in origin

	var fwg sync.WaitGroup
	const followers = 3
	for i := 0; i < followers; i++ {
		fwg.Add(1)
		go func() {
			defer fwg.Done()
			mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", target, nil))
		}()
	}
	// White-box wait: a fixed sleep here raced follower scheduling — a starved
	// follower set would make the leader's WriteHeader see waiters==0 and stream
	// in lockstep (raw headers), silently skipping the decoupled path under test.
	// The helper drives a single in-flight fill, so c.locks holds exactly one entry.
	require.Eventually(t, func() bool {
		c.lockMu.Lock()
		defer c.lockMu.Unlock()
		for _, l := range c.locks {
			if l.waiters.Load() == followers {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond, "followers blocked on the fill lock")

	close(originGate)
	<-leaderDone
	fwg.Wait()
	return leaderRec, atomic.LoadInt32(&calls)
}

// Under contention the decoupled leader's own response has hop-by-hop headers
// stripped and no Age header (the body is served from the buffer, not the HIT path).
func TestCache_DecoupleFill_LeaderHeadersSanitized(t *testing.T) {
	eachBackendDecoupled(t, func(t *testing.T, c *Cache) {
		leaderRec, calls := decoupledContended(t, c, "http://acme.com/s", func(w http.ResponseWriter) {
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("X-App", "yes")
			w.Header().Set("Content-Length", "2")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("ok"))
		})
		assert.EqualValues(t, 1, calls, "origin contacted once")
		assert.Equal(t, "MISS", leaderRec.Header().Get("X-Cache"))
		assert.Equal(t, "ok", leaderRec.Body.String())
		assert.Equal(t, "yes", leaderRec.Header().Get("X-App"), "end-to-end header kept")
		assert.Equal(t, "", leaderRec.Header().Get("Connection"), "hop-by-hop stripped from the decoupled leader")
		assert.Equal(t, "", leaderRec.Header().Get("Age"), "a MISS carries no Age header")
	})
}

// Without contention (no follower waiting), DecoupleFill streams in lockstep — the
// leader's raw headers pass straight through, with no buffering.
func TestCache_DecoupleFill_SoloUsesLockstep(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024, DecoupleFill: true})
	var calls int32
	h := origin(originSpec{body: []byte("ok"), header: hdr("Cache-Control", "max-age=60", "Connection", "keep-alive")}, &calls)
	r := do(c, h, "GET", "http://acme.com/solo", nil)
	assert.Equal(t, "MISS", r.Header().Get("X-Cache"))
	assert.Equal(t, "ok", r.Body.String())
	assert.Equal(t, "keep-alive", r.Header().Get("Connection"), "uncontended fill streams in lockstep (raw headers)")
}

// A mid-fill storage write failure must still deliver the full body to the leader
// (it's buffered independently of storage); the response is just not cached.
func TestCache_DecoupleFill_StorageWriteErrorStillServesBody(t *testing.T) {
	c := New(failingWriterStorage{NewMemory(1 << 20)}, Options{MaxFileSize: 1 << 20, DecoupleFill: true})
	var calls int32
	h := origin(originSpec{body: []byte("payload"), header: hdr("Cache-Control", "max-age=60")}, &calls)
	r := do(c, h, "GET", "http://acme.com/e", nil)
	assert.Equal(t, "MISS", r.Header().Get("X-Cache"))
	assert.Equal(t, "payload", r.Body.String(), "leader gets the full body despite the storage write error")
}

// A handler that Flushes mid-response during a decoupled fill must not prematurely
// commit the client: the flush is ignored and the full body is still served+cached.
func TestCache_DecoupleFill_FlushDuringFillIgnored(t *testing.T) {
	eachBackendDecoupled(t, func(t *testing.T, c *Cache) {
		var calls int32
		h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Cache-Control", "max-age=60")
			w.Header().Set("Content-Length", "6")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("abc"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush() // must be a no-op while buffering
			}
			_, _ = w.Write([]byte("def"))
		})
		r1 := do(c, h, "GET", "http://acme.com/f", nil)
		assert.Equal(t, "MISS", r1.Header().Get("X-Cache"))
		assert.Equal(t, "abcdef", r1.Body.String(), "full body served despite the mid-fill flush")
		r2 := do(c, h, "GET", "http://acme.com/f", nil)
		assert.Equal(t, "HIT", r2.Header().Get("X-Cache"))
		assert.Equal(t, "abcdef", r2.Body.String())
		assert.EqualValues(t, 1, atomic.LoadInt32(&calls))
	})
}
