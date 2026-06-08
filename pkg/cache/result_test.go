package cache

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resultRecorder captures every ResultInfo the OnResult hook reports, safely
// across the foreground request and any background goroutine.
type resultRecorder struct {
	mu    sync.Mutex
	infos []ResultInfo
}

func (rr *resultRecorder) fn() ResultFunc {
	return func(_ *http.Request, info ResultInfo) {
		rr.mu.Lock()
		rr.infos = append(rr.infos, info)
		rr.mu.Unlock()
	}
}

func (rr *resultRecorder) all() []ResultInfo {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return append([]ResultInfo(nil), rr.infos...)
}

func (rr *resultRecorder) results() []Result {
	out := make([]Result, 0)
	for _, i := range rr.all() {
		out = append(out, i.Result)
	}
	return out
}

func newRecordedCache(opts Options, rr *resultRecorder) *Cache {
	opts.OnResult = rr.fn()
	if opts.MaxFileSize == 0 {
		opts.MaxFileSize = 1024
	}
	return New(NewMemory(1<<20), opts)
}

func TestCache_OnResult_Bypass(t *testing.T) {
	t.Run("non-cacheable method", func(t *testing.T) {
		var rr resultRecorder
		c := newRecordedCache(Options{}, &rr)
		o := origin(originSpec{body: []byte("x"), header: hdr("Cache-Control", "max-age=60")}, new(int32))

		do(c, o, "POST", "http://acme.com/a", nil)

		assert.Equal(t, []Result{ResultBypass}, rr.results())
		assert.Zero(t, rr.all()[0].FillDuration, "bypass is not a fill")
	})

	t.Run("Cacheable returns false", func(t *testing.T) {
		var rr resultRecorder
		c := newRecordedCache(Options{Cacheable: func(*http.Request) bool { return false }}, &rr)
		o := origin(originSpec{body: []byte("x"), header: hdr("Cache-Control", "max-age=60")}, new(int32))

		do(c, o, "GET", "http://acme.com/a", nil)

		assert.Equal(t, []Result{ResultBypass}, rr.results())
	})
}

func TestCache_OnResult_MissThenHit(t *testing.T) {
	var rr resultRecorder
	c := newRecordedCache(Options{}, &rr)
	o := origin(originSpec{body: []byte("hello"), header: hdr("Cache-Control", "max-age=60"), sleep: time.Millisecond}, new(int32))

	do(c, o, "GET", "http://acme.com/a", nil)
	do(c, o, "GET", "http://acme.com/a", nil)

	infos := rr.all()
	require.Len(t, infos, 2)
	assert.Equal(t, ResultMiss, infos[0].Result)
	assert.Positive(t, infos[0].FillDuration, "a miss reports the origin-fill duration")
	assert.Equal(t, ResultHit, infos[1].Result)
	assert.Zero(t, infos[1].FillDuration, "a hit contacts no origin")
}

func TestCache_OnResult_StaleWhileRevalidate(t *testing.T) {
	var rr resultRecorder
	c := newRecordedCache(Options{}, &rr)
	seedStale(t, c, "GET", "/x", staleMeta(5*time.Second, 300*time.Second, 0), []byte("old"))

	var calls int32
	o := origin(originSpec{body: []byte("new"), header: hdr("Cache-Control", "max-age=300")}, &calls)

	rec := do(c, o, "GET", "/x", nil)
	require.Equal(t, "STALE", rec.Header().Get("X-Cache"))

	// The single foreground serve reports exactly one STALE with no fill duration —
	// the refresh is detached.
	infos := rr.all()
	require.Len(t, infos, 1)
	assert.Equal(t, ResultStale, infos[0].Result)
	assert.Zero(t, infos[0].FillDuration)

	// The background revalidation contacts the origin but must NOT report — it has no
	// client request to attribute. After it lands, still exactly one recorded result.
	require.Eventually(t, func() bool { return atomic.LoadInt32(&calls) == 1 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond) // let any (erroneous) report settle
	assert.Len(t, rr.all(), 1, "the detached revalidation goroutine never reports a result")
}

func TestCache_OnResult_StaleIfError(t *testing.T) {
	t.Run("origin errors -> STALE_ERROR", func(t *testing.T) {
		var rr resultRecorder
		c := newRecordedCache(Options{}, &rr)
		seedStale(t, c, "GET", "/x", staleMeta(5*time.Second, 0, 300*time.Second), []byte("old"))

		o := origin(originSpec{status: http.StatusInternalServerError, body: []byte("boom"), sleep: time.Millisecond}, new(int32))
		rec := do(c, o, "GET", "/x", nil)
		require.Equal(t, "old", rec.Body.String())

		infos := rr.all()
		require.Len(t, infos, 1)
		assert.Equal(t, ResultStaleError, infos[0].Result)
		assert.Positive(t, infos[0].FillDuration, "the failed origin attempt is still a fill")
	})

	t.Run("origin recovers -> MISS", func(t *testing.T) {
		var rr resultRecorder
		c := newRecordedCache(Options{}, &rr)
		seedStale(t, c, "GET", "/x", staleMeta(5*time.Second, 0, 300*time.Second), []byte("old"))

		o := origin(originSpec{body: []byte("new"), header: hdr("Cache-Control", "max-age=300")}, new(int32))
		rec := do(c, o, "GET", "/x", nil)
		require.Equal(t, "new", rec.Body.String())

		assert.Equal(t, []Result{ResultMiss}, rr.results(), "a successful revalidation is a normal miss")
	})
}

// A nil OnResult must be a no-op on every path (the zero-overhead default).
func TestCache_OnResult_NilIsNoOp(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1024})
	o := origin(originSpec{body: []byte("x"), header: hdr("Cache-Control", "max-age=60")}, new(int32))
	assert.NotPanics(t, func() {
		do(c, o, "GET", "http://acme.com/a", nil) // miss
		do(c, o, "GET", "http://acme.com/a", nil) // hit
		do(c, o, "POST", "http://acme.com/a", nil) // bypass
	})
}
