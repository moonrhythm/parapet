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
