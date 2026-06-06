package cache

import (
	"net/http"
	"net/http/httptest"
	"strconv"
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
func eachBackend(t *testing.T, fn func(t *testing.T, c *Cache)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) {
		fn(t, New(NewMemory(1<<20), Options{MaxFileSize: 1024}))
	})
	t.Run("disk", func(t *testing.T) {
		d, err := NewDisk(t.TempDir(), 1<<20)
		require.NoError(t, err)
		fn(t, New(d, Options{MaxFileSize: 1024}))
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
func TestCache_LockTimeoutConfigurable(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024, LockTimeout: 20 * time.Millisecond})
	var calls int32
	h := origin(originSpec{body: []byte("slow"), header: hdr("Cache-Control", "max-age=60"), sleep: 200 * time.Millisecond}, &calls)
	mw := c.ServeHandler(h)

	const n = 4
	var wg, start sync.WaitGroup
	start.Add(1)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait()
			rec := httptest.NewRecorder()
			mw.ServeHTTP(rec, httptest.NewRequest("GET", "http://acme.com/s", nil))
			assert.Equal(t, "slow", rec.Body.String())
		}()
	}
	start.Done()
	wg.Wait()
	// The leader's 200ms fill far exceeds the 20ms timeout, so followers don't wait
	// for it — they each contact the origin.
	assert.Greater(t, atomic.LoadInt32(&calls), int32(1), "short LockTimeout: followers fetch the origin instead of waiting")
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
		storePut(t, c.storage, key, Meta{
			Status: 200, Header: http.Header{}, PrimaryHex: primary,
			Created:    time.Now().Add(-30 * time.Second).UnixNano(),
			FreshUntil: time.Now().Add(time.Hour).UnixNano(), Size: 3,
		}, []byte("abc"))

		r := do(c, http.NotFoundHandler(), "GET", "http://acme.com/age", nil)
		require.Equal(t, "HIT", r.Header().Get("X-Cache"))
		age, err := strconv.Atoi(r.Header().Get("Age"))
		require.NoError(t, err)
		assert.GreaterOrEqual(t, age, 30)
		assert.LessOrEqual(t, age, 31)
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
