package cache

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedStale writes an entry directly under the key the cache derives for
// (method, target), so a test can place an already-stale entry without waiting
// for real time to pass. body is the stored body; the Meta times/windows are the
// caller's to set.
func seedStale(t *testing.T, c *Cache, method, target string, m Meta, body []byte) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	key := c.variantHash(c.primaryHash(req), req)
	if m.Header == nil {
		m.Header = http.Header{"Content-Type": {"text/plain"}}
	}
	m.Size = int64(len(body))
	storePut(t, c.storage, key, m, body)
}

// staleMeta builds Meta for an entry that went stale staleAgo in the past, with
// the given RFC 5861 windows (seconds past FreshUntil).
func staleMeta(staleAgo time.Duration, swr, sie time.Duration) Meta {
	now := time.Now()
	return Meta{
		Status:               http.StatusOK,
		Created:              now.Add(-staleAgo - time.Second).UnixNano(),
		FreshUntil:           now.Add(-staleAgo).UnixNano(),
		StaleWhileRevalidate: int64(swr),
		StaleIfError:         int64(sie),
	}
}

func TestCache_StaleWhileRevalidate(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		seedStale(t, c, "GET", "/x", staleMeta(5*time.Second, 300*time.Second, 0), []byte("old"))

		var calls int32
		o := origin(originSpec{
			body:   []byte("new"),
			header: http.Header{"Cache-Control": {"max-age=300"}},
		}, &calls)

		// First request is served the stale body immediately and kicks off a
		// background revalidation.
		rec := do(c, o, "GET", "/x", nil)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "STALE", rec.Header().Get("X-Cache"))
		assert.Equal(t, "old", rec.Body.String())

		// The background revalidation fetches the origin exactly once.
		require.Eventually(t, func() bool { return atomic.LoadInt32(&calls) == 1 },
			2*time.Second, 5*time.Millisecond)

		// The cache now holds the fresh entry: a later request HITs it.
		require.Eventually(t, func() bool {
			return do(c, o, "GET", "/x", nil).Header().Get("X-Cache") == "HIT"
		}, 2*time.Second, 5*time.Millisecond)

		rec = do(c, o, "GET", "/x", nil)
		assert.Equal(t, "HIT", rec.Header().Get("X-Cache"))
		assert.Equal(t, "new", rec.Body.String())
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "revalidation must be single-flighted")
	})
}

// The background revalidation must run on a context detached from the original
// request, so it does not share (and race) request-scoped mutable state such as
// the logger's per-request record.
func TestCache_StaleWhileRevalidate_DetachedContext(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024})
	seedStale(t, c, "GET", "/x", staleMeta(5*time.Second, 300*time.Second, 0), []byte("old"))

	type ctxKey struct{}
	var sawValue atomic.Bool
	var calls int32
	o := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(ctxKey{}) != nil {
			sawValue.Store(true)
		}
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Cache-Control", "max-age=300")
		w.Header().Set("Content-Length", "3")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("new"))
	})

	mw := c.ServeHandler(o)
	req := httptest.NewRequest("GET", "/x", nil).
		WithContext(context.WithValue(context.Background(), ctxKey{}, "sentinel"))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	require.Equal(t, "STALE", rec.Header().Get("X-Cache"))

	require.Eventually(t, func() bool { return atomic.LoadInt32(&calls) == 1 },
		2*time.Second, 5*time.Millisecond)
	assert.False(t, sawValue.Load(), "background revalidation must not inherit the request context values")
}

// A panic in the origin during background revalidation must be contained (the
// http.Server's per-request recover does not cover this detached goroutine), and
// the fill lock must still be released so the cache keeps working.
func TestCache_StaleWhileRevalidate_PanicContained(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024})
	seedStale(t, c, "GET", "/x", staleMeta(5*time.Second, 300*time.Second, 0), []byte("old"))

	revaled := make(chan struct{})
	var once sync.Once
	bad := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(revaled) })
		panic("boom during revalidation")
	})

	rec := do(c, bad, "GET", "/x", nil)
	assert.Equal(t, "STALE", rec.Header().Get("X-Cache"), "client is unaffected by the revalidation panic")

	select {
	case <-revaled:
	case <-time.After(2 * time.Second):
		t.Fatal("background revalidation never ran")
	}
	time.Sleep(50 * time.Millisecond) // let the deferred recover/release finish

	// If the panic were uncontained, the test binary would already have crashed.
	// Prove the cache still works (the lock was released) via a healthy fill.
	var calls int32
	good := origin(originSpec{body: []byte("ok"), header: http.Header{"Cache-Control": {"max-age=300"}}}, &calls)
	rec = do(c, good, "GET", "/y", nil)
	assert.Equal(t, "MISS", rec.Header().Get("X-Cache"))
	assert.Equal(t, "ok", rec.Body.String())
}

func TestCache_StaleWhileRevalidate_SingleFlight(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024})
	seedStale(t, c, "GET", "/x", staleMeta(5*time.Second, 300*time.Second, 0), []byte("old"))

	var calls int32
	o := origin(originSpec{
		body:   []byte("new"),
		header: http.Header{"Cache-Control": {"max-age=300"}},
		sleep:  30 * time.Millisecond, // hold the revalidation so all requests race it
	}, &calls)

	const n = 12
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			do(c, o, "GET", "/x", nil) // each served STALE (or HIT once refreshed)
		}()
	}
	close(start)
	wg.Wait()

	require.Eventually(t, func() bool { return atomic.LoadInt32(&calls) == 1 },
		2*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "concurrent stale hits collapse to one revalidation")
}

func TestCache_StaleWhileRevalidate_PastWindowIsMiss(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		// Stale 10s ago, SWR window only 5s, no SIE -> fully expired.
		seedStale(t, c, "GET", "/x", staleMeta(10*time.Second, 5*time.Second, 0), []byte("old"))

		var calls int32
		o := origin(originSpec{
			body:   []byte("new"),
			header: http.Header{"Cache-Control": {"max-age=300"}},
		}, &calls)

		rec := do(c, o, "GET", "/x", nil)
		assert.Equal(t, "MISS", rec.Header().Get("X-Cache"))
		assert.Equal(t, "new", rec.Body.String())
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "expired entry is a synchronous miss")
	})
}

func TestCache_StaleIfError_ServesStaleOnError(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		// Stale, past any SWR, but within stale-if-error.
		seedStale(t, c, "GET", "/x", staleMeta(5*time.Second, 0, 300*time.Second), []byte("old"))

		var calls int32
		o := origin(originSpec{status: http.StatusInternalServerError, body: []byte("boom")}, &calls)

		rec := do(c, o, "GET", "/x", nil)
		assert.Equal(t, http.StatusOK, rec.Code, "origin 5xx is replaced by the stale entry")
		assert.Equal(t, "STALE", rec.Header().Get("X-Cache"))
		assert.Equal(t, "old", rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "boom")
		assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"), "stale entry's stored headers are served")
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls))

		// The stale entry survives the failed revalidation: a second error still
		// serves it (it was not deleted).
		rec = do(c, o, "GET", "/x", nil)
		assert.Equal(t, "STALE", rec.Header().Get("X-Cache"))
		assert.Equal(t, "old", rec.Body.String())
	})
}

func TestCache_StaleIfError_ServesFreshOnSuccess(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		seedStale(t, c, "GET", "/x", staleMeta(5*time.Second, 0, 300*time.Second), []byte("old"))

		var calls int32
		o := origin(originSpec{
			body:   []byte("new"),
			header: http.Header{"Cache-Control": {"max-age=300"}},
		}, &calls)

		// A healthy origin: the revalidated response is served and cached.
		rec := do(c, o, "GET", "/x", nil)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "MISS", rec.Header().Get("X-Cache"))
		assert.Equal(t, "new", rec.Body.String())

		rec = do(c, o, "GET", "/x", nil)
		assert.Equal(t, "HIT", rec.Header().Get("X-Cache"))
		assert.Equal(t, "new", rec.Body.String())
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
	})
}

// backdateFiles sets every file under dir to an mtime old enough that the disk
// startup scan's age gate (reapMinAge) would permit reaping it.
func backdateFiles(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return os.Chtimes(path, old, old)
		}
		return nil
	}))
}

// A stale-but-within-SIE entry must survive the disk startup rescan, otherwise
// stale-if-error silently loses its fallback after a restart. The scan is run
// synchronously here (NewDisk's is a background goroutine) so the reap decision is
// deterministic.
func TestCache_StaleIfError_SurvivesDiskRescan(t *testing.T) {
	dir := t.TempDir()

	d1, err := NewDisk(dir, 1<<20)
	require.NoError(t, err)
	c1 := New(d1, Options{MaxFileSize: 1024})
	req := httptest.NewRequest("GET", "/x", nil)
	key := c1.variantHash(c1.primaryHash(req), req)
	// Stale 5s ago, SIE window 300s -> serveable for ~5 more minutes.
	seedStale(t, c1, "GET", "/x", staleMeta(5*time.Second, 0, 300*time.Second), []byte("old"))

	// Age the files past the reap age-gate so the scan would be free to delete a
	// genuinely-expired entry, then rescan.
	backdateFiles(t, dir, 2*time.Minute)
	d2, err := NewDisk(dir, 1<<20)
	require.NoError(t, err)
	d2.scan(time.Now())

	_, _, ok := d2.Get(key)
	assert.True(t, ok, "a stale-but-within-SIE entry must survive the disk rescan")
}

func TestCache_StaleIfError_PastWindowPropagatesError(t *testing.T) {
	eachBackend(t, func(t *testing.T, c *Cache) {
		// Stale 10s ago, SIE only 5s -> expired; the origin error reaches the client.
		seedStale(t, c, "GET", "/x", staleMeta(10*time.Second, 0, 5*time.Second), []byte("old"))

		var calls int32
		o := origin(originSpec{status: http.StatusBadGateway, body: []byte("boom")}, &calls)

		rec := do(c, o, "GET", "/x", nil)
		assert.Equal(t, http.StatusBadGateway, rec.Code)
		assert.Equal(t, "boom", rec.Body.String())
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
	})
}

func TestCache_StaleServing_InvalidatedOverrides(t *testing.T) {
	c := New(NewMemory(1<<20), Options{
		MaxFileSize:      1024,
		InvalidatedAfter: func(_ *http.Request, _ Meta) int64 { return time.Now().UnixNano() },
	})
	seedStale(t, c, "GET", "/x", staleMeta(5*time.Second, 300*time.Second, 300*time.Second), []byte("old"))

	var calls int32
	o := origin(originSpec{
		body:   []byte("new"),
		header: http.Header{"Cache-Control": {"max-age=300"}},
	}, &calls)

	// Invalidation forces a synchronous miss; the stale entry is never served.
	rec := do(c, o, "GET", "/x", nil)
	assert.Equal(t, "MISS", rec.Header().Get("X-Cache"))
	assert.Equal(t, "new", rec.Body.String())
}

// The InvalidatedAfter hook is consulted only for an entry that would be served
// (fresh or stale-serveable), not for a time-expired one being reaped.
func TestCache_InvalidatedAfter_NotCalledOnExpired(t *testing.T) {
	var hookCalls int32
	c := New(NewMemory(1<<20), Options{
		MaxFileSize: 1024,
		InvalidatedAfter: func(_ *http.Request, _ Meta) int64 {
			atomic.AddInt32(&hookCalls, 1)
			return 0
		},
	})
	seedStale(t, c, "GET", "/x", staleMeta(10*time.Second, 0, 0), []byte("old")) // fully expired

	o := origin(originSpec{body: []byte("new"), header: http.Header{"Cache-Control": {"max-age=300"}}}, new(int32))
	rec := do(c, o, "GET", "/x", nil)
	assert.Equal(t, "MISS", rec.Header().Get("X-Cache"))
	assert.Equal(t, int32(0), atomic.LoadInt32(&hookCalls), "hook must not run on a time-expired entry")
}

// storedMeta returns the Meta the cache stored for (method, target).
func storedMeta(t *testing.T, c *Cache, method, target string) (Meta, bool) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	m, _, ok := c.storage.Get(c.variantHash(c.primaryHash(req), req))
	return m, ok
}

func TestCache_DefaultStaleWindows(t *testing.T) {
	t.Run("applied when origin omits them, and not leaked to the client", func(t *testing.T) {
		c := New(NewMemory(1<<20), Options{
			MaxFileSize:                 1024,
			DefaultStaleWhileRevalidate: 300 * time.Second,
			DefaultStaleIfError:         600 * time.Second,
		})
		o := origin(originSpec{body: []byte("hi"), header: http.Header{"Cache-Control": {"max-age=60"}}}, new(int32))

		rec := do(c, o, "GET", "/x", nil)
		require.Equal(t, "MISS", rec.Header().Get("X-Cache"))
		assert.Equal(t, "max-age=60", rec.Header().Get("Cache-Control"), "forced windows must not appear in the served header")

		m, ok := storedMeta(t, c, "GET", "/x")
		require.True(t, ok)
		assert.Equal(t, int64(300*time.Second), m.StaleWhileRevalidate)
		assert.Equal(t, int64(600*time.Second), m.StaleIfError)

		rec = do(c, o, "GET", "/x", nil)
		require.Equal(t, "HIT", rec.Header().Get("X-Cache"))
		assert.Equal(t, "max-age=60", rec.Header().Get("Cache-Control"))
	})

	t.Run("explicit origin directive wins", func(t *testing.T) {
		c := New(NewMemory(1<<20), Options{MaxFileSize: 1024, DefaultStaleWhileRevalidate: 300 * time.Second})
		o := origin(originSpec{body: []byte("hi"), header: http.Header{"Cache-Control": {"max-age=60, stale-while-revalidate=10"}}}, new(int32))

		do(c, o, "GET", "/x", nil)
		m, ok := storedMeta(t, c, "GET", "/x")
		require.True(t, ok)
		assert.Equal(t, int64(10*time.Second), m.StaleWhileRevalidate, "origin's window wins over the default")
	})

	t.Run("must-revalidate suppresses the default", func(t *testing.T) {
		c := New(NewMemory(1<<20), Options{
			MaxFileSize:                 1024,
			DefaultStaleWhileRevalidate: 300 * time.Second,
			DefaultStaleIfError:         600 * time.Second,
		})
		o := origin(originSpec{body: []byte("hi"), header: http.Header{"Cache-Control": {"max-age=60, must-revalidate"}}}, new(int32))

		do(c, o, "GET", "/x", nil)
		m, ok := storedMeta(t, c, "GET", "/x")
		require.True(t, ok)
		assert.Zero(t, m.StaleWhileRevalidate)
		assert.Zero(t, m.StaleIfError)
	})
}

// End-to-end: a default stale-if-error window (origin sends none) drives the
// serve-stale-on-error behavior.
func TestCache_DefaultStaleIfError_ServesStaleOnError(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024, DefaultStaleIfError: 300 * time.Second})

	fill := origin(originSpec{body: []byte("old"), header: http.Header{"Cache-Control": {"max-age=60"}}}, new(int32))
	do(c, fill, "GET", "/x", nil) // MISS, stored with the default SIE window

	// Age the stored entry into staleness (keep its windows) without waiting.
	req := httptest.NewRequest("GET", "/x", nil)
	key := c.variantHash(c.primaryHash(req), req)
	m, body, ok := c.storage.Get(key)
	require.True(t, ok)
	require.Equal(t, int64(300*time.Second), m.StaleIfError)
	m.FreshUntil = time.Now().Add(-5 * time.Second).UnixNano()
	storePut(t, c.storage, key, m, body)

	bad := origin(originSpec{status: http.StatusInternalServerError, body: []byte("boom")}, new(int32))
	rec := do(c, bad, "GET", "/x", nil)
	assert.Equal(t, "STALE", rec.Header().Get("X-Cache"))
	assert.Equal(t, "old", rec.Body.String())
}

func TestPolicy_StaleWindows(t *testing.T) {
	now := time.Now()
	h := func(cc string) http.Header {
		return http.Header{"Cache-Control": {cc}, "Content-Length": {"3"}}
	}

	t.Run("parsed", func(t *testing.T) {
		d := decide("GET", 200, h("max-age=60, stale-while-revalidate=120, stale-if-error=3600"), false, 1<<20, now)
		require.True(t, d.cacheable)
		assert.Equal(t, 120*time.Second, d.staleWhileRevalidate)
		assert.Equal(t, 3600*time.Second, d.staleIfError)
	})

	t.Run("must-revalidate suppresses both", func(t *testing.T) {
		d := decide("GET", 200, h("max-age=60, must-revalidate, stale-while-revalidate=120, stale-if-error=3600"), false, 1<<20, now)
		require.True(t, d.cacheable)
		assert.Zero(t, d.staleWhileRevalidate)
		assert.Zero(t, d.staleIfError)
	})

	t.Run("proxy-revalidate suppresses both", func(t *testing.T) {
		d := decide("GET", 200, h("s-maxage=60, proxy-revalidate, stale-while-revalidate=120"), false, 1<<20, now)
		require.True(t, d.cacheable)
		assert.Zero(t, d.staleWhileRevalidate)
		assert.Zero(t, d.staleIfError)
	})

	t.Run("absent", func(t *testing.T) {
		d := decide("GET", 200, h("max-age=60"), false, 1<<20, now)
		require.True(t, d.cacheable)
		assert.Zero(t, d.staleWhileRevalidate)
		assert.Zero(t, d.staleIfError)
	})
}
