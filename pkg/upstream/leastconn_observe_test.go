package upstream

import (
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shedRecorder captures the reasons OnShed fired with.
type shedRecorder struct {
	mu sync.Mutex
	r  []ShedReason
}

func (s *shedRecorder) fn() ShedFunc {
	return func(r ShedReason) {
		s.mu.Lock()
		s.r = append(s.r, r)
		s.mu.Unlock()
	}
}

func (s *shedRecorder) all() []ShedReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ShedReason(nil), s.r...)
}

func TestShedReasonString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "empty", ShedEmpty.String())
	assert.Equal(t, "saturated", ShedSaturated.String())
	assert.Equal(t, "all_dark", ShedAllDark.String())
	assert.Equal(t, "empty", ShedReason(99).String(), "an out-of-range value never produces an unbounded label")
}

func TestInflightSnapshotInitial(t *testing.T) {
	t.Parallel()
	l := NewLeastConnLoadBalancer([]*Target{
		{Host: "t0", Transport: freshBody(), MaxConcurrent: 3},
		{Host: "t1", Transport: freshBody()}, // unbounded
	})
	got := l.Inflight() // forces init; no traffic, no panic on a fresh balancer
	require.Len(t, got, 2)
	assert.Equal(t, TargetLoad{Host: "t0", Active: 0, Cap: 3}, got[0])
	assert.Equal(t, TargetLoad{Host: "t1", Active: 0, Cap: 0}, got[1], "unbounded target reports Cap 0")
}

func TestInflightSnapshotLive(t *testing.T) {
	t.Parallel()
	tr := &blockingTransport{release: make(chan struct{})}
	l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: tr, MaxConcurrent: 3}})

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
			if err == nil && resp != nil && resp.Body != nil {
				resp.Body.Close() // releases the slot (dec on body close)
			}
		}()
	}
	// Both requests claimed a slot and are now blocked in the transport.
	require.Eventually(t, func() bool { return l.Inflight()[0].Active == 2 }, time.Second, time.Millisecond,
		"the snapshot reflects live claims")
	close(tr.release)
	wg.Wait()
	require.Eventually(t, func() bool { return l.Inflight()[0].Active == 0 }, time.Second, time.Millisecond,
		"the snapshot reflects the dec when the body is closed")
}

func TestOnShedSaturated(t *testing.T) {
	t.Parallel()
	var rec shedRecorder
	l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: freshBody(), MaxConcurrent: 1}})
	l.OnShed = rec.fn()
	l.once.Do(l.init)
	l.peers[0].active.Store(1) // pin the single slot at cap

	resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
	assert.Nil(t, resp)
	assert.Equal(t, ErrUnavailable, err)
	assert.Equal(t, []ShedReason{ShedSaturated}, rec.all(), "one shed, reason saturated")

	// Free the slot; a request now succeeds and fires NO shed.
	l.peers[0].active.Store(0)
	resp2, err2 := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
	require.NoError(t, err2)
	if resp2 != nil && resp2.Body != nil {
		resp2.Body.Close()
	}
	assert.Equal(t, []ShedReason{ShedSaturated}, rec.all(), "a successful pick fires no shed")
}

func TestOnShedEmpty(t *testing.T) {
	t.Parallel()
	var rec shedRecorder
	l := NewLeastConnLoadBalancer(nil)
	l.OnShed = rec.fn()

	resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
	assert.Nil(t, resp)
	assert.Equal(t, ErrUnavailable, err)
	assert.Equal(t, []ShedReason{ShedEmpty}, rec.all())
}

func TestOnShedAllDarkVsSaturated(t *testing.T) {
	t.Parallel()

	// All three reach pick's give-up path with the single target at cap; only the
	// active-HC gate state distinguishes saturated (gate up / no gate) from all_dark
	// (gate down — the fail-open re-scan still finds nothing admittable).
	newSaturated := func() (*LeastConnLoadBalancer, *shedRecorder) {
		rec := &shedRecorder{}
		l := NewLeastConnLoadBalancer([]*Target{{Host: "t0", Transport: freshBody(), MaxConcurrent: 1}})
		l.OnShed = rec.fn()
		l.once.Do(l.init)
		l.peers[0].active.Store(1) // at cap
		return l, rec
	}

	t.Run("gate down and saturated -> all_dark", func(t *testing.T) {
		t.Parallel()
		l, rec := newSaturated()
		gate := make([]atomic.Bool, 1) // zero value false == down
		l.setHealthGate(gate)
		_, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Equal(t, ErrUnavailable, err)
		assert.Equal(t, []ShedReason{ShedAllDark}, rec.all())
	})

	t.Run("gate up and saturated -> saturated", func(t *testing.T) {
		t.Parallel()
		l, rec := newSaturated()
		gate := make([]atomic.Bool, 1)
		gate[0].Store(true) // up
		l.setHealthGate(gate)
		_, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Equal(t, ErrUnavailable, err)
		assert.Equal(t, []ShedReason{ShedSaturated}, rec.all())
	})

	t.Run("no gate and saturated -> saturated", func(t *testing.T) {
		t.Parallel()
		l, rec := newSaturated()
		_, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Equal(t, ErrUnavailable, err)
		assert.Equal(t, []ShedReason{ShedSaturated}, rec.all())
	})
}
