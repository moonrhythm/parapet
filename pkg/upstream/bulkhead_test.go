package upstream

import (
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freshBody is a transport that returns a 200 with a NEW closable body per call,
// so each in-flight request owns its own slot release.
func freshBody() funcTransport {
	return func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: &countingBody{}, Header: http.Header{}}, nil
	}
}

// blockingTransport holds every request inside RoundTrip until release is closed,
// so the test can pin a known number of slots as in-flight and observe the peak
// concurrency the transport ever saw (maxSeen) — the witness for the hard cap.
type blockingTransport struct {
	release  chan struct{}
	inflight atomic.Int64
	maxSeen  atomic.Int64
}

func (t *blockingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	cur := t.inflight.Add(1)
	for { // record the running max without losing a concurrent bump
		m := t.maxSeen.Load()
		if cur <= m || t.maxSeen.CompareAndSwap(m, cur) {
			break
		}
	}
	<-t.release // hold the slot until the test lets go
	t.inflight.Add(-1)
	return &http.Response{StatusCode: 200, Body: &countingBody{}, Header: http.Header{}}, nil
}

func TestLeastConnBulkhead(t *testing.T) {
	t.Parallel()

	// The core invariant: under a concurrent burst far exceeding the cap, the
	// transport never sees more than MaxConcurrent in-flight at once, and the
	// surplus is shed (ErrUnavailable) rather than queued or over-admitted.
	t.Run("HardCapNeverExceeded", func(t *testing.T) {
		t.Parallel()
		const N, K = 24, 3
		tr := &blockingTransport{release: make(chan struct{})}
		l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: tr, MaxConcurrent: K}})

		var shed atomic.Int64
		var wg sync.WaitGroup
		for range N {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
				if err == ErrUnavailable {
					shed.Add(1)
					return
				}
				assert.NoError(t, err)
				if resp != nil && resp.Body != nil {
					resp.Body.Close()
				}
			}()
		}

		// K winners block inside the transport; the surplus is shed. Wait for both
		// to settle, then check the witness — never poll a snapshot mid-flight.
		require.Eventually(t, func() bool { return tr.inflight.Load() == int64(K) },
			2*time.Second, 5*time.Millisecond, "exactly K requests reach the (blocking) target")
		require.Eventually(t, func() bool { return shed.Load() == int64(N-K) },
			2*time.Second, 5*time.Millisecond, "the surplus N-K is shed, not queued")

		assert.LessOrEqual(t, tr.maxSeen.Load(), int64(K),
			"the transport NEVER saw more than the cap in-flight")
		assert.LessOrEqual(t, l.peers[0].active.Load(), int64(K), "active is bounded by the cap")

		close(tr.release) // let the K winners finish and release their slots
		wg.Wait()
		assert.EqualValues(t, 0, l.peers[0].active.Load(), "every slot released after bodies closed")
	})

	// A target at its cap is skipped: traffic routes to an under-cap sibling instead
	// of overloading the saturated one.
	t.Run("RoutesAroundCappedTarget", func(t *testing.T) {
		t.Parallel()
		capped := &recordingTransport{}
		free := &recordingTransport{}
		l := NewLeastConnLoadBalancer([]*Target{
			{Host: "capped", Transport: capped, MaxConcurrent: 1},
			{Host: "free", Transport: free},
		})

		// First request lands on the capped target (rotation starts at index 0) and is
		// held in-flight, filling its single slot.
		held, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		require.NoError(t, err)
		require.Equal(t, map[string]int{"capped": 1}, capped.counts())

		// Every subsequent request must route around the full target to "free".
		for range 5 {
			resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
			require.NoError(t, err)
			resp.Body.Close()
		}
		assert.Equal(t, map[string]int{"capped": 1}, capped.counts(), "no new traffic to the full target")
		assert.Equal(t, map[string]int{"free": 5}, free.counts(), "surplus routed to the under-cap target")

		held.Body.Close() // release the slot
		assert.EqualValues(t, 0, l.peers[0].active.Load())
	})

	// When EVERY target is at its cap, the balancer sheds rather than overloading a
	// saturated origin — and a freed slot immediately re-admits.
	t.Run("AllAtCapShedThenReadmit", func(t *testing.T) {
		t.Parallel()
		l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: freshBody(), MaxConcurrent: 2}})

		r1, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil)) // active 1
		require.NoError(t, err)
		r2, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil)) // active 2 == cap
		require.NoError(t, err)
		require.EqualValues(t, 2, l.peers[0].active.Load())

		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil)) // at cap -> shed
		assert.Nil(t, resp)
		assert.Equal(t, ErrUnavailable, err, "a fully-capped balancer sheds with ErrUnavailable")

		r1.Body.Close() // free one slot
		assert.EqualValues(t, 1, l.peers[0].active.Load(), "body close releases the slot")

		resp, err = l.RoundTrip(httptest.NewRequest("GET", "/", nil)) // slot available again
		require.NoError(t, err, "a freed slot re-admits")
		resp.Body.Close()
		r2.Body.Close()
		assert.EqualValues(t, 0, l.peers[0].active.Load())
	})

	// A non-positive MaxConcurrent is the unbounded default: the uncapped fast path,
	// behaviorally identical to a target with no cap set.
	t.Run("ZeroMaxConcurrentIsUnbounded", func(t *testing.T) {
		t.Parallel()
		l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: freshBody(), MaxConcurrent: 0}})
		var held []*http.Response
		for range 8 {
			resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
			require.NoError(t, err)
			held = append(held, resp) // do not close: pile them all in-flight
		}
		assert.EqualValues(t, 8, l.peers[0].active.Load(), "cap 0 imposes no limit")
		for _, resp := range held {
			resp.Body.Close()
		}
		assert.EqualValues(t, 0, l.peers[0].active.Load())
	})

	// The cap must be HARD even on the error path: a transport error releases the
	// slot inline (no body to own), so a stream of failures never latches the cap.
	t.Run("ErrorPathReleasesSlot", func(t *testing.T) {
		t.Parallel()
		l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: errTransport{}, MaxConcurrent: 1}})
		for range 5 {
			resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
			assert.Nil(t, resp)
			assert.Error(t, err)
		}
		assert.EqualValues(t, 0, l.peers[0].active.Load(), "each error released its slot; the cap never latched")
	})

	// Two targets with DIFFERENT caps are each bounded independently: under a burst
	// far exceeding total capacity, neither transport ever sees more than ITS OWN
	// cap, total admitted == sum(caps), and only the surplus is shed. This is the
	// per-target isolation the bulkhead promises, and (unlike the serial
	// route-around test) it drives the concurrent skip/re-scan path with two capped
	// peers competing.
	t.Run("IndependentPerTargetCaps", func(t *testing.T) {
		t.Parallel()
		const N, K0, K1 = 30, 3, 5
		t0 := &blockingTransport{release: make(chan struct{})}
		t1 := &blockingTransport{release: make(chan struct{})}
		l := NewLeastConnLoadBalancer([]*Target{
			{Host: "t0", Transport: t0, MaxConcurrent: K0},
			{Host: "t1", Transport: t1, MaxConcurrent: K1},
		})

		var shed atomic.Int64
		var wg sync.WaitGroup
		for range N {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
				if err == ErrUnavailable {
					shed.Add(1)
					return
				}
				assert.NoError(t, err)
				if resp != nil && resp.Body != nil {
					resp.Body.Close()
				}
			}()
		}

		// Both targets fill to exactly their own cap (no slot can shed while one is
		// free), and the surplus N-(K0+K1) is shed.
		require.Eventually(t, func() bool {
			return t0.inflight.Load() == int64(K0) && t1.inflight.Load() == int64(K1)
		}, 2*time.Second, 5*time.Millisecond, "each target fills to its own cap")
		require.Eventually(t, func() bool { return shed.Load() == int64(N-K0-K1) },
			2*time.Second, 5*time.Millisecond, "only the surplus beyond sum(caps) is shed")

		assert.LessOrEqual(t, t0.maxSeen.Load(), int64(K0), "t0 never exceeded its own cap")
		assert.LessOrEqual(t, t1.maxSeen.Load(), int64(K1), "t1 never exceeded its own cap")

		close(t0.release)
		close(t1.release)
		wg.Wait()
		assert.EqualValues(t, 0, l.peers[0].active.Load())
		assert.EqualValues(t, 0, l.peers[1].active.Load())
	})

	// Weight and cap coexist: the cap clips a heavy target's weighted share. A
	// Weight-2 target would otherwise carry 2x the concurrency, but its cap of 2
	// bounds it, so the surplus the weight would have pulled lands on the lighter
	// (higher-cap) target instead.
	t.Run("WeightClippedByCap", func(t *testing.T) {
		t.Parallel()
		l := NewLeastConnLoadBalancer([]*Target{
			{Host: "heavy", Transport: freshBody(), Weight: 2, MaxConcurrent: 2},
			{Host: "light", Transport: freshBody(), Weight: 1, MaxConcurrent: 4},
		})
		var held []*http.Response
		for range 6 { // exactly sum(caps): every request is admitted, filling both
			resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
			require.NoError(t, err)
			held = append(held, resp) // hold in-flight
		}
		assert.EqualValues(t, 2, l.peers[0].active.Load(), "heavy clipped at its cap despite weight 2")
		assert.EqualValues(t, 4, l.peers[1].active.Load(), "surplus the weight would pull lands on light")

		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Nil(t, resp)
		assert.Equal(t, ErrUnavailable, err, "both at cap -> shed")

		for _, resp := range held {
			resp.Body.Close()
		}
		assert.EqualValues(t, 0, l.peers[0].active.Load())
		assert.EqualValues(t, 0, l.peers[1].active.Load())
	})

	// Regression for the rotation-cursor overflow: when the tie-break cursor wraps
	// near MaxUint32, the index scan must still visit EVERY peer. With 3 peers (not
	// a power of two) and two at their cap, the lone under-cap peer must still be
	// picked — a uint32 mid-scan wrap would alias an index, skip it, and false-shed.
	t.Run("RotationCursorWrapNoFalseShed", func(t *testing.T) {
		t.Parallel()
		l := NewLeastConnLoadBalancer([]*Target{
			{Host: "t0", Transport: freshBody(), MaxConcurrent: 5},
			{Host: "t1", Transport: freshBody(), MaxConcurrent: 5},
			{Host: "t2", Transport: freshBody(), MaxConcurrent: 5},
		})
		l.once.Do(l.init)          // build peers before poking state directly
		l.peers[0].active.Store(5) // at cap
		l.peers[1].active.Store(5) // at cap
		l.i.Store(math.MaxUint32)  // next pick computes start = Add(1)-1 = MaxUint32

		p, ok, _ := l.pick(3)
		require.True(t, ok, "the lone under-cap peer must be found despite the cursor wrap")
		assert.Same(t, &l.peers[2], p, "t2 (the only under-cap peer) is selected, not a false shed")
	})
}
