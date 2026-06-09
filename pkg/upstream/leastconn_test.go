package upstream

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// funcTransport adapts a function to http.RoundTripper, for returning crafted
// responses (fresh per call).
type funcTransport func(*http.Request) (*http.Response, error)

func (f funcTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// errTransport always fails before a response (no body to own).
type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: connection refused")
}

// countingBody is an io.ReadCloser that counts its Close calls.
type countingBody struct{ closes atomic.Int32 }

func (b *countingBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b *countingBody) Close() error             { b.closes.Add(1); return nil }

// rwcBody is an io.ReadWriteCloser, as the body of a 101 Switching Protocols
// upgrade response is (httputil.ReverseProxy type-asserts it).
type rwcBody struct{ closed atomic.Bool }

func (b *rwcBody) Read([]byte) (int, error)    { return 0, io.EOF }
func (b *rwcBody) Write(p []byte) (int, error) { return len(p), nil }
func (b *rwcBody) Close() error                { b.closed.Store(true); return nil }

func bodyResp(status int, body io.ReadCloser) funcTransport {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: body, Header: http.Header{}}, nil
	}
}

func TestLeastConnLoadBalancer(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		l := NewLeastConnLoadBalancer(nil)
		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Nil(t, resp)
		assert.Equal(t, ErrUnavailable, err)
	})

	t.Run("SingleTargetBalancesToZero", func(t *testing.T) {
		rec := &recordingTransport{}
		l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: rec}})
		driveLB(l, 5)
		assert.Equal(t, map[string]int{"t0": 5}, rec.counts())
		assert.EqualValues(t, 0, l.peers[0].active.Load(), "every request closed -> no in-flight")
	})

	t.Run("EqualLoadIsRoundRobin", func(t *testing.T) {
		rec := &recordingTransport{}
		l := NewLeastConnLoadBalancer([]*Target{
			{Host: "t0", Transport: rec}, {Host: "t1", Transport: rec}, {Host: "t2", Transport: rec},
		})
		driveLB(l, 6) // each body closed, so every pick sees all-zero load
		assert.Equal(t, []string{"t0", "t1", "t2", "t0", "t1", "t2"}, rec.hits)
	})

	t.Run("WeightedConcurrencyShare", func(t *testing.T) {
		// weight 2 vs 1, three requests held in-flight: the heavy target carries 2x.
		l := NewLeastConnLoadBalancer([]*Target{
			{Host: "heavy", Transport: bodyResp(200, &countingBody{}), Weight: 2},
			{Host: "light", Transport: bodyResp(200, &countingBody{}), Weight: 1},
		})
		var held []*http.Response
		for range 3 {
			resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
			require.NoError(t, err)
			held = append(held, resp) // do NOT close: keep it in-flight
		}
		assert.EqualValues(t, 2, l.peers[0].active.Load(), "weight-2 holds two in-flight")
		assert.EqualValues(t, 1, l.peers[1].active.Load(), "weight-1 holds one in-flight")

		for _, resp := range held {
			resp.Body.Close()
		}
		assert.EqualValues(t, 0, l.peers[0].active.Load())
		assert.EqualValues(t, 0, l.peers[1].active.Load())
	})

	t.Run("ExactlyOnceOnSuccessDoubleClose", func(t *testing.T) {
		body := &countingBody{}
		l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: bodyResp(200, body)}})
		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		require.NoError(t, err)
		assert.EqualValues(t, 1, l.peers[0].active.Load())

		require.NoError(t, resp.Body.Close())
		_ = resp.Body.Close() // double close
		assert.EqualValues(t, 0, l.peers[0].active.Load(), "active decremented once despite double close")
		assert.EqualValues(t, 2, body.closes.Load(), "underlying Close forwarded each time")
	})

	t.Run("ExactlyOnceOnError", func(t *testing.T) {
		l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: errTransport{}}})
		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Nil(t, resp)
		assert.Error(t, err)
		assert.EqualValues(t, 0, l.peers[0].active.Load(), "error path decremented inline, no leak")
	})

	t.Run("PreservesReadWriteCloserFor101", func(t *testing.T) {
		// The WebSocket/upgrade regression guard: the default recorder body is NOT an
		// io.ReadWriteCloser, so this MUST supply one, or the lcRWCBody path is never
		// exercised and the guard tests nothing.
		body := &rwcBody{}
		l := NewLeastConnLoadBalancer([]*Target{
			{Host: "t0", Transport: bodyResp(http.StatusSwitchingProtocols, body)},
		})
		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		require.NoError(t, err)

		_, ok := resp.Body.(io.ReadWriteCloser)
		assert.True(t, ok, "101 upgrade body must stay an io.ReadWriteCloser for ReverseProxy")
		assert.EqualValues(t, 1, l.peers[0].active.Load())

		require.NoError(t, resp.Body.Close())
		assert.True(t, body.closed.Load(), "underlying upgrade conn closed")
		assert.EqualValues(t, 0, l.peers[0].active.Load())
	})

	t.Run("NilBodyGuard", func(t *testing.T) {
		l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: bodyResp(204, nil)}})
		assert.NotPanics(t, func() {
			resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
			require.NoError(t, err)
			require.NotNil(t, resp)
		})
		assert.EqualValues(t, 0, l.peers[0].active.Load(), "nil-body response decremented inline")
	})

	t.Run("PanicSafetyReleasesActive", func(t *testing.T) {
		// A nil Transport panics inside RoundTrip after the increment; the deferred
		// release must unwind it, or least-conn would progressively black-hole the peer.
		l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: nil}})
		assert.Panics(t, func() {
			_, _ = l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		})
		assert.EqualValues(t, 0, l.peers[0].active.Load(), "panic must not leak the in-flight count")
	})

	t.Run("NoLeakUnderConcurrencyMixedErrors", func(t *testing.T) {
		// Mixed success and error targets, heavy concurrency, every body closed: each
		// attempt balances its own inc/dec (this also covers the retry re-entry path),
		// so all counters return to zero.
		l := NewLeastConnLoadBalancer([]*Target{
			{Host: "ok", Transport: &recordingTransport{}},
			{Host: "bad", Transport: errTransport{}},
		})
		var wg sync.WaitGroup
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				driveLB(l, 50)
			}()
		}
		wg.Wait()
		assert.EqualValues(t, 0, l.peers[0].active.Load(), "ok target balanced")
		assert.EqualValues(t, 0, l.peers[1].active.Load(), "error target balanced")
	})
}

func TestEffectiveWeight(t *testing.T) {
	t.Parallel()
	assert.EqualValues(t, 1, effectiveWeight(&Target{Weight: 0}), "unset weight defaults to 1")
	assert.EqualValues(t, 1, effectiveWeight(&Target{Weight: -5}), "negative weight defaults to 1")
	assert.EqualValues(t, 3, effectiveWeight(&Target{Weight: 3}))
}
