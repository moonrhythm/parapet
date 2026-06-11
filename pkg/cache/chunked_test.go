package cache

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// chunkedOrigin serves body with the given headers but DELIBERATELY no
// Content-Length — the teeWriter sees a chunked/streamed response. extra runs
// after the body is written (e.g. to panic, simulating a truncated upstream).
func chunkedOrigin(body []byte, hdr http.Header, calls *int32, extra func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		for k, vs := range hdr {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		if extra != nil {
			extra()
		}
	})
}

func TestCache_Chunked_CachedWhenEnabled(t *testing.T) {
	body := []byte("console.log('hi')")
	hdr := http.Header{"Cache-Control": {"public, max-age=60"}, "Content-Type": {"text/javascript"}}

	t.Run("enabled: no-Content-Length GET is cached", func(t *testing.T) {
		c := New(NewMemory(1<<20), Options{MaxFileSize: 1 << 20, CacheChunked: true})
		var calls int32
		o := chunkedOrigin(body, hdr, &calls, nil)

		rec1 := do(c, o, "GET", "/app.js", nil)
		assert.Equal(t, "MISS", rec1.Header().Get("X-Cache"))

		rec2 := do(c, o, "GET", "/app.js", nil)
		assert.Equal(t, "HIT", rec2.Header().Get("X-Cache"))
		assert.Equal(t, body, rec2.Body.Bytes())
		// The HIT carries a synthesized Content-Length even though the origin sent none.
		assert.Equal(t, strconv.Itoa(len(body)), rec2.Header().Get("Content-Length"))
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "second request served from cache")
	})

	t.Run("disabled (default): no-Content-Length GET is never cached", func(t *testing.T) {
		c := New(NewMemory(1<<20), Options{MaxFileSize: 1 << 20}) // CacheChunked off
		var calls int32
		o := chunkedOrigin(body, hdr, &calls, nil)

		assert.Equal(t, "MISS", do(c, o, "GET", "/app.js", nil).Header().Get("X-Cache"))
		assert.Equal(t, "MISS", do(c, o, "GET", "/app.js", nil).Header().Get("X-Cache"))
		assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "chunked stays uncached when disabled")
	})
}

func TestCache_Chunked_EventStreamNeverBuffered(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1 << 20, CacheChunked: true})
	var calls int32
	// Fresh + cacheable status, but an SSE content-type must never be buffered.
	o := chunkedOrigin([]byte("data: hi\n\n"),
		http.Header{"Cache-Control": {"public, max-age=60"}, "Content-Type": {"text/event-stream"}}, &calls, nil)

	assert.Equal(t, "MISS", do(c, o, "GET", "/events", nil).Header().Get("X-Cache"))
	assert.Equal(t, "MISS", do(c, o, "GET", "/events", nil).Header().Get("X-Cache"))
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "event-stream must not be cached")
}

func TestCache_Chunked_OverCapNotCached(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 8, CacheChunked: true})
	body := []byte("way longer than eight bytes")
	var calls int32
	o := chunkedOrigin(body, http.Header{"Cache-Control": {"public, max-age=60"}}, &calls, nil)

	rec1 := do(c, o, "GET", "/big.js", nil)
	assert.Equal(t, "MISS", rec1.Header().Get("X-Cache"))
	assert.Equal(t, body, rec1.Body.Bytes(), "over-cap body still served in full")

	rec2 := do(c, o, "GET", "/big.js", nil)
	assert.Equal(t, "MISS", rec2.Header().Get("X-Cache"))
	assert.Equal(t, body, rec2.Body.Bytes())
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "over-cap chunked body is not cached")
}

// A truncated upstream surfaces as a panic (httputil.ReverseProxy emits
// http.ErrAbortHandler); the leader's deferred cleanup must abort the entry so a
// partial body is never committed and the next request re-fetches.
func TestCache_Chunked_TruncationNotCommitted(t *testing.T) {
	c := New(NewMemory(1<<20), Options{MaxFileSize: 1 << 20, CacheChunked: true})
	var calls int32
	o := chunkedOrigin([]byte("partial"), http.Header{"Cache-Control": {"public, max-age=60"}}, &calls,
		func() { panic(http.ErrAbortHandler) })

	doRecover := func() {
		defer func() { _ = recover() }() // swallow the simulated abort, as net/http's server would
		do(c, o, "GET", "/x.js", nil)
	}
	doRecover()
	doRecover()
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "a truncated (panicked) fill must not be cached")
}
