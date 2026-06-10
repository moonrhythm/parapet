package upstream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctxFake is a transport whose latency honors request-context cancellation, so a
// cancelled losing leg aborts (as a real transport does).
type ctxFake struct {
	calls  atomic.Int64
	delay  time.Duration
	err    error
	status int
}

func (t *ctxFake) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	if t.err != nil {
		return nil, t.err
	}
	if t.delay > 0 {
		select {
		case <-time.After(t.delay):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	w := httptest.NewRecorder()
	if t.status != 0 {
		w.WriteHeader(t.status)
	}
	_, _ = w.Write([]byte("ok"))
	return w.Result(), nil
}

func rrLB(trs ...http.RoundTripper) *RoundRobinLoadBalancer {
	targets := make([]*Target, len(trs))
	for i, tr := range trs {
		targets[i] = &Target{Host: "t" + string(rune('0'+i)), Transport: tr}
	}
	return NewRoundRobinLoadBalancer(targets)
}

func TestHedging_DisabledIsPassThrough(t *testing.T) {
	t.Parallel()
	f := &ctxFake{delay: 30 * time.Millisecond}
	h := NewHedgingLoadBalancer(rrLB(f)) // HedgeDelay 0 -> disabled
	resp, err := h.RoundTrip(httptest.NewRequest("GET", "/", nil))
	require.NoError(t, err)
	resp.Body.Close()
	assert.EqualValues(t, 1, f.calls.Load(), "no hedge when HedgeDelay<=0")
}

func TestHedging_NonHedgeablePassThrough(t *testing.T) {
	t.Parallel()
	f := &ctxFake{}
	h := NewHedgingLoadBalancer(rrLB(f))
	h.HedgeDelay = 10 * time.Millisecond
	// POST is not idempotent/body-less -> not hedgeable.
	resp, err := h.RoundTrip(httptest.NewRequest("POST", "/", strings.NewReader("x")))
	require.NoError(t, err)
	resp.Body.Close()
	assert.EqualValues(t, 1, f.calls.Load())
}

func TestHedging_AlreadyRetryingPassThrough(t *testing.T) {
	t.Parallel()
	f := &ctxFake{delay: 30 * time.Millisecond}
	h := NewHedgingLoadBalancer(rrLB(f, &ctxFake{}))
	h.HedgeDelay = 5 * time.Millisecond
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), retryContextKey{}, 1)) // inside the retry loop
	resp, err := h.RoundTrip(r)
	require.NoError(t, err)
	resp.Body.Close()
	assert.EqualValues(t, 1, f.calls.Load(), "a retried request is not also hedged")
}

func TestHedging_HedgeWinsAndCopiesBackHost(t *testing.T) {
	t.Parallel()
	slow := &ctxFake{delay: time.Second}
	fast := &ctxFake{}
	h := NewHedgingLoadBalancer(rrLB(slow, fast)) // primary -> t0(slow), hedge -> t1(fast)
	h.HedgeDelay = 15 * time.Millisecond

	r := httptest.NewRequest("GET", "/", nil)
	resp, err := h.RoundTrip(r)
	require.NoError(t, err)
	resp.Body.Close()

	assert.EqualValues(t, 1, slow.calls.Load())
	assert.EqualValues(t, 1, fast.calls.Load())
	assert.Equal(t, "t1", r.URL.Host, "the winning target's host is copied back for OnRoundTrip/logging")
}

func TestHedging_PrimaryWinsNoHedge(t *testing.T) {
	t.Parallel()
	fast := &ctxFake{}
	hedge := &ctxFake{}
	h := NewHedgingLoadBalancer(rrLB(fast, hedge))
	// The no-hedge property must hold at any scheduler speed: a real 200ms timer can
	// beat a stalled primary leg under -race contention and spuriously launch the
	// hedge, so make the timer unreachable instead of racing it.
	h.HedgeDelay = time.Hour
	resp, err := h.RoundTrip(httptest.NewRequest("GET", "/", nil))
	require.NoError(t, err)
	resp.Body.Close()
	assert.EqualValues(t, 1, fast.calls.Load())
	assert.EqualValues(t, 0, hedge.calls.Load(), "primary won before HedgeDelay -> no hedge launched")
}

func TestHedging_FailFastOnError(t *testing.T) {
	t.Parallel()
	boom := &ctxFake{err: errors.New("dial fail")} // primary errors immediately
	fast := &ctxFake{}
	h := NewHedgingLoadBalancer(rrLB(boom, fast))
	h.HedgeDelay = 10 * time.Second // long: only fail-fast can make the hedge fire quickly
	h.HedgeOnError = true

	start := time.Now()
	resp, err := h.RoundTrip(httptest.NewRequest("GET", "/", nil))
	elapsed := time.Since(start)
	require.NoError(t, err, "the hedge wins after the primary errors")
	resp.Body.Close()
	assert.EqualValues(t, 1, boom.calls.Load(), "the primary leg ran (and errored)")
	assert.EqualValues(t, 1, fast.calls.Load(), "exactly one hedge leg served the winner")
	// Bound the duration against the config, not a magic constant: the old
	// 200ms-vs-500ms discriminator was bridgeable by scheduler stalls alone. A broken
	// fail-fast fires the hedge on the ~10s timer and fails this cleanly, while the
	// pass-side jitter margin is now 5s — out of reach of any realistic stall.
	assert.Less(t, elapsed, h.HedgeDelay/2, "the hedge fired on the error, not after HedgeDelay")
}

func TestHedging_AllFailReturnsError(t *testing.T) {
	t.Parallel()
	h := NewHedgingLoadBalancer(rrLB(&ctxFake{err: errors.New("e0")}, &ctxFake{err: errors.New("e1")}))
	h.HedgeDelay = 10 * time.Millisecond
	resp, err := h.RoundTrip(httptest.NewRequest("GET", "/", nil))
	assert.Nil(t, resp)
	assert.Error(t, err, "no winner -> surfaces the first error for the ErrorHandler")
}

// rwcStream is a writable body (io.ReadWriteCloser) for the 101-upgrade path.
type rwcStream struct{ closed atomic.Bool }

func (s *rwcStream) Read([]byte) (int, error)    { return 0, io.EOF }
func (s *rwcStream) Write(p []byte) (int, error) { return len(p), nil }
func (s *rwcStream) Close() error                { s.closed.Store(true); return nil }

func TestHedging_PreservesReadWriteCloserWinner(t *testing.T) {
	t.Parallel()
	body := &rwcStream{}
	upgrade := funcTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusSwitchingProtocols, Body: body, Header: http.Header{}}, nil
	})
	slow := &ctxFake{delay: time.Second}
	h := NewHedgingLoadBalancer(rrLB(slow, upgrade))
	h.HedgeDelay = 15 * time.Millisecond

	resp, err := h.RoundTrip(httptest.NewRequest("GET", "/", nil))
	require.NoError(t, err)
	_, ok := resp.Body.(io.ReadWriteCloser)
	assert.True(t, ok, "a 101 upgrade winner body must remain an io.ReadWriteCloser")
	require.NoError(t, resp.Body.Close())
	assert.True(t, body.closed.Load(), "underlying upgrade body closed")
}

func TestHedging_Concurrent(t *testing.T) {
	t.Parallel()
	slow := &ctxFake{delay: 30 * time.Millisecond}
	fast := &ctxFake{}
	h := NewHedgingLoadBalancer(rrLB(slow, fast))
	h.HedgeDelay = 5 * time.Millisecond
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := h.RoundTrip(httptest.NewRequest("GET", "/", nil))
			if err == nil && resp != nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
}

// THE regression guard for the winner-truncation bug. Recorder-based fakes return a
// body NOT bound to any request context, so they cannot catch it; this races real
// servers over a real transport, where the response body's lifetime IS the request
// context. If the winner's leg were cancelled (the original shared-context design),
// io.ReadAll would return a truncated body + context.Canceled.
func TestHedging_StreamingWinnerBodyNotTruncated(t *testing.T) {
	t.Parallel()
	const chunks, chunkSize = 40, 1024
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, _ := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		for range chunks {
			_, _ = w.Write(bytes.Repeat([]byte("x"), chunkSize))
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(time.Millisecond) // body keeps streaming after headers arrive
		}
	}))
	defer stream.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second): // always loses
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()

	tr := &HTTPTransport{}
	// primary -> t0 (slow, loses), hedge -> t1 (streaming, wins).
	lb := NewRoundRobinLoadBalancer([]*Target{
		{Host: strings.TrimPrefix(slow.URL, "http://"), Transport: tr},
		{Host: strings.TrimPrefix(stream.URL, "http://"), Transport: tr},
	})
	h := NewHedgingLoadBalancer(lb)
	h.HedgeDelay = 20 * time.Millisecond

	resp, err := h.RoundTrip(httptest.NewRequest("GET", "http://x/", nil))
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, err, "the winner body must read fully — not aborted by cancelling the loser")
	assert.Equal(t, chunks*chunkSize, len(body), "the entire winner body reaches the caller")
}
