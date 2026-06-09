package upstream

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cbStateOf reads a breaker's current state.
func cbStateOf(l *CircuitBreakingLoadBalancer, i int) uint64 {
	_, _, s := cbUnpack(l.breakers[i].word.Load())
	return s
}

// hammer drives a balancer from many goroutines for a fixed duration.
func hammer(l http.RoundTripper, goroutines int, dur time.Duration) {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, _ := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
				if resp != nil {
					resp.Body.Close()
				}
			}
		}()
	}
	time.Sleep(dur)
	close(stop)
	wg.Wait()
}

func TestCircuitBreakingLoadBalancer(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		l := NewCircuitBreakingLoadBalancer(nil)
		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Nil(t, resp)
		assert.Equal(t, ErrUnavailable, err)
	})

	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()
		l := NewCircuitBreakingLoadBalancer(newEjectTargets(&fakeUpstream{}))
		driveLB(l, 1)
		assert.Equal(t, defaultFailureThreshold, l.FailureThreshold)
		assert.Equal(t, defaultSuccessThreshold, l.SuccessThreshold)
		assert.Equal(t, defaultCBOpenTimeout, l.OpenTimeout)
		assert.Equal(t, defaultCBMaxOpenTimeout, l.MaxOpenTimeout)
		assert.Equal(t, defaultProbeTimeout, l.ProbeTimeout)
		assert.EqualValues(t, defaultHalfOpenMaxProbes, l.HalfOpenMaxProbes)
	})

	t.Run("RoundRobinWhenHealthy", func(t *testing.T) {
		t.Parallel()
		t0, t1 := &fakeUpstream{}, &fakeUpstream{}
		l := NewCircuitBreakingLoadBalancer(newEjectTargets(t0, t1))
		driveLB(l, 10)
		assert.Equal(t, int64(5), t0.calls.Load())
		assert.Equal(t, int64(5), t1.calls.Load())
	})

	t.Run("TripsAfterFailureThreshold", func(t *testing.T) {
		t.Parallel()
		t0 := &fakeUpstream{}
		t0.down.Store(true)
		l := &CircuitBreakingLoadBalancer{Targets: newEjectTargets(t0), FailureThreshold: 3}
		driveLB(l, 3)
		assert.Equal(t, cbOpen, cbStateOf(l, 0), "tripped after 3 consecutive failures")
		assert.Equal(t, int64(3), t0.calls.Load())
	})

	t.Run("OpenFailsFastNoRoundTrip", func(t *testing.T) {
		t.Parallel()
		t0 := &fakeUpstream{}
		t0.down.Store(true)
		l := &CircuitBreakingLoadBalancer{Targets: newEjectTargets(t0), FailureThreshold: 3} // default 5s cooldown
		driveLB(l, 3)                                                                        // trip
		require.Equal(t, int64(3), t0.calls.Load())

		driveLB(l, 10) // open + cooling: every pick skips, no round-trip
		assert.Equal(t, int64(3), t0.calls.Load(), "an open target receives no round-trips")
	})

	t.Run("StateMachineWalk", func(t *testing.T) {
		t.Parallel()
		t0 := &fakeUpstream{}
		t0.down.Store(true)
		l := &CircuitBreakingLoadBalancer{
			Targets: newEjectTargets(t0), FailureThreshold: 3, SuccessThreshold: 2, OpenTimeout: 20 * time.Millisecond,
		}
		driveLB(l, 3)
		require.Equal(t, cbOpen, cbStateOf(l, 0), "CLOSED -> OPEN")

		t0.down.Store(false)
		time.Sleep(40 * time.Millisecond) // cooldown expires
		driveLB(l, 1)
		assert.Equal(t, cbHalfOpen, cbStateOf(l, 0), "OPEN -> HALF-OPEN (one sub-threshold probe)")

		driveLB(l, 1)
		assert.Equal(t, cbClosed, cbStateOf(l, 0), "HALF-OPEN -> CLOSED (SuccessThreshold probes)")
	})

	t.Run("ProbeFailureReopensWithLongerBackoff", func(t *testing.T) {
		t.Parallel()
		t0 := &fakeUpstream{}
		t0.down.Store(true)
		l := &CircuitBreakingLoadBalancer{
			Targets: newEjectTargets(t0), FailureThreshold: 1, OpenTimeout: 20 * time.Millisecond, MaxOpenTimeout: time.Hour,
		}
		driveLB(l, 1) // trip; generations == 1
		g1 := l.breakers[0].generations.Load()

		time.Sleep(40 * time.Millisecond) // cooldown
		driveLB(l, 1)                     // probe fails (still down) -> re-open
		assert.Greater(t, l.breakers[0].generations.Load(), g1, "a failed probe re-opens with a longer backoff")
		assert.Equal(t, cbOpen, cbStateOf(l, 0))
	})

	t.Run("AllOpenReturnsUnavailable", func(t *testing.T) {
		t.Parallel()
		t0, t1 := &fakeUpstream{}, &fakeUpstream{}
		t0.down.Store(true)
		t1.down.Store(true)
		l := &CircuitBreakingLoadBalancer{Targets: newEjectTargets(t0, t1), FailureThreshold: 1, OpenTimeout: time.Hour}
		driveLB(l, 2) // trip both
		c0, c1 := t0.calls.Load(), t1.calls.Load()

		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Nil(t, resp)
		assert.Equal(t, ErrUnavailable, err, "all-open sheds load (503), does not fail open")
		assert.Equal(t, c0, t0.calls.Load(), "no round-trip to an open target")
		assert.Equal(t, c1, t1.calls.Load())
	})

	t.Run("SuccessResetsFailures", func(t *testing.T) {
		t.Parallel()
		t0 := &fakeUpstream{}
		t0.down.Store(true)
		l := &CircuitBreakingLoadBalancer{Targets: newEjectTargets(t0), FailureThreshold: 3}
		driveLB(l, 2) // 2 failures, below threshold
		t0.down.Store(false)
		driveLB(l, 1) // success
		assert.Zero(t, l.breakers[0].failures.Load(), "a success clears the consecutive failure count")
		assert.Equal(t, cbClosed, cbStateOf(l, 0))
	})

	t.Run("IgnoresClientCancel", func(t *testing.T) {
		t.Parallel()
		l := &CircuitBreakingLoadBalancer{Targets: newEjectTargets(&fakeUpstream{}), FailureThreshold: 3}
		l.once.Do(l.init)
		for range 10 {
			l.record(&l.breakers[0], 0, cbAdmitClosed, nil, fmt.Errorf("canceled: %w", context.Canceled))
		}
		assert.Zero(t, l.breakers[0].failures.Load())
		assert.Equal(t, cbClosed, cbStateOf(l, 0))
	})

	t.Run("IsFailureHookCountsStatus", func(t *testing.T) {
		t.Parallel()
		t0, t1 := &fakeUpstream{status: 500}, &fakeUpstream{}
		l := &CircuitBreakingLoadBalancer{
			Targets: newEjectTargets(t0, t1), FailureThreshold: 3,
			IsFailure: func(resp *http.Response, err error) bool {
				return err != nil || (resp != nil && resp.StatusCode >= 500)
			},
		}
		driveLB(l, 6) // t0 returns 500 three times -> trips
		before := t0.calls.Load()
		driveLB(l, 8)
		assert.Equal(t, before, t0.calls.Load(), "the 5xx target is open and skipped")
	})
}

func TestCircuitBreakerBackoff(t *testing.T) {
	t.Parallel()
	l := &CircuitBreakingLoadBalancer{OpenTimeout: time.Second, MaxOpenTimeout: 5 * time.Second}
	l.once.Do(l.init)
	assert.Equal(t, time.Second, l.openTimeout(1))
	assert.Equal(t, 2*time.Second, l.openTimeout(2))
	assert.Equal(t, 4*time.Second, l.openTimeout(3))
	assert.Equal(t, 5*time.Second, l.openTimeout(4), "capped at MaxOpenTimeout")
	assert.Equal(t, 5*time.Second, l.openTimeout(100), "no overflow on large generations")
}

func TestCircuitBreaker_ConcurrentBurstIsOneTrip(t *testing.T) {
	t.Parallel()
	// A burst of simultaneous threshold-crossing failures must collapse to a single
	// trip (the from-state CAS guard) — not one trip per late failure.
	t0 := &fakeUpstream{}
	t0.down.Store(true)
	l := &CircuitBreakingLoadBalancer{Targets: newEjectTargets(t0), FailureThreshold: 1}
	l.once.Do(l.init)
	b := &l.breakers[0]

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.record(b, 0, cbAdmitClosed, nil, errors.New("down"))
		}()
	}
	wg.Wait()

	assert.Equal(t, cbOpen, cbStateOf(l, 0))
	assert.EqualValues(t, 1, b.generations.Load(), "a concurrent failure burst is exactly one trip")
}

// concProbeTransport records the maximum number of round-trips running
// concurrently while its breaker is HALF-OPEN — i.e. the live probe concurrency.
type concProbeTransport struct {
	l        *CircuitBreakingLoadBalancer
	down     atomic.Bool
	inflight atomic.Int32
	maxHalf  atomic.Int32
}

func (t *concProbeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	_, _, state := cbUnpack(t.l.breakers[0].word.Load())
	if state == cbHalfOpen {
		cur := t.inflight.Add(1)
		for {
			m := t.maxHalf.Load()
			if cur <= m || t.maxHalf.CompareAndSwap(m, cur) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		t.inflight.Add(-1)
	}
	if t.down.Load() {
		return nil, errors.New("down")
	}
	return httptest.NewRecorder().Result(), nil
}

func TestCircuitBreaker_HalfOpenProbeCapUnderHerd(t *testing.T) {
	t.Parallel()
	// The load-bearing concurrency proof: at cap=1 exactly one probe is ever in
	// flight, even as a herd hammers the open->half-open edge. (A separate-atomic
	// probe counter leaked 32-64 here; the packed word holds it at 1.)
	tr := &concProbeTransport{}
	tr.down.Store(true)
	l := &CircuitBreakingLoadBalancer{
		Targets:  []*Target{{Host: "u", Transport: tr}},
		FailureThreshold: 1, OpenTimeout: time.Millisecond, MaxOpenTimeout: 2 * time.Millisecond, HalfOpenMaxProbes: 1,
	}
	tr.l = l
	driveLB(l, 1) // trip
	hammer(l, 32, 100*time.Millisecond)

	assert.LessOrEqual(t, tr.maxHalf.Load(), int32(1), "half-open probe cap=1 holds under a herd")
	assert.GreaterOrEqual(t, tr.maxHalf.Load(), int32(1), "half-open was actually exercised")
}

func TestCircuitBreaker_HalfOpenProbeCap3(t *testing.T) {
	t.Parallel()
	// cap>1: the per-generation admission cap is an upper bound. SuccessThreshold is
	// huge so successful probes stay in one HALF-OPEN generation and concurrency can
	// build toward the cap.
	tr := &concProbeTransport{}
	tr.down.Store(true)
	l := &CircuitBreakingLoadBalancer{
		Targets:  []*Target{{Host: "u", Transport: tr}},
		FailureThreshold: 1, SuccessThreshold: 1 << 30,
		OpenTimeout: time.Millisecond, MaxOpenTimeout: 2 * time.Millisecond, HalfOpenMaxProbes: 3,
	}
	tr.l = l
	driveLB(l, 1)        // trip while down
	tr.down.Store(false) // probes now succeed (sub-threshold) -> target stays half-open
	hammer(l, 40, 150*time.Millisecond)

	assert.LessOrEqual(t, tr.maxHalf.Load(), int32(3), "never exceeds HalfOpenMaxProbes")
	assert.GreaterOrEqual(t, tr.maxHalf.Load(), int32(1), "half-open was exercised")
}

// panicProbeTransport panics when admitted as a HALF-OPEN probe.
type panicProbeTransport struct{ l *CircuitBreakingLoadBalancer }

func (t *panicProbeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	_, _, state := cbUnpack(t.l.breakers[0].word.Load())
	if state == cbHalfOpen {
		panic("probe boom")
	}
	return nil, errors.New("down")
}

func TestCircuitBreaker_ProbeSlotPanicSafety(t *testing.T) {
	t.Parallel()
	// A probe whose RoundTrip panics must release its slot on unwind, or a cap=1
	// breaker wedges half-open forever.
	tr := &panicProbeTransport{}
	l := &CircuitBreakingLoadBalancer{Targets: []*Target{{Host: "u", Transport: tr}}, FailureThreshold: 1, OpenTimeout: 10 * time.Millisecond}
	tr.l = l
	driveLB(l, 1) // trip (CLOSED failure)
	time.Sleep(20 * time.Millisecond)

	assert.Panics(t, func() {
		_, _ = l.RoundTrip(httptest.NewRequest("GET", "/", nil)) // edge -> probe panics
	})

	_, probes, state := cbUnpack(l.breakers[0].word.Load())
	assert.Equal(t, cbHalfOpen, state)
	assert.Zero(t, probes, "the panicked probe released its slot (not wedged)")
}

// hangProbeTransport blocks the first HALF-OPEN probe until released, simulating a
// hung backend with no transport timeout.
type hangProbeTransport struct {
	l       *CircuitBreakingLoadBalancer
	release chan struct{}
	hung    atomic.Bool
}

func (t *hangProbeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	_, _, state := cbUnpack(t.l.breakers[0].word.Load())
	if state == cbHalfOpen && t.hung.CompareAndSwap(false, true) {
		<-t.release
	}
	return nil, errors.New("down")
}

func TestCircuitBreaker_HungProbeReclaim(t *testing.T) {
	t.Parallel()
	// A hung probe (never-returning round-trip) must not wedge the target half-open:
	// after ProbeTimeout its slot is reclaimed and the target re-opens. The deferred
	// release covers panics, NOT hung calls — this is the separate safety net.
	tr := &hangProbeTransport{release: make(chan struct{})}
	defer close(tr.release) // unblock the hung goroutine at test end
	l := &CircuitBreakingLoadBalancer{
		Targets: []*Target{{Host: "u", Transport: tr}}, FailureThreshold: 1, OpenTimeout: 20 * time.Millisecond, ProbeTimeout: 30 * time.Millisecond,
	}
	tr.l = l
	driveLB(l, 1) // trip -> OPEN (generation 1)
	time.Sleep(30 * time.Millisecond)

	go func() { _, _ = l.RoundTrip(httptest.NewRequest("GET", "/", nil)) }() // admits a probe, then hangs
	require.Eventually(t, func() bool {
		_, probes, state := cbUnpack(l.breakers[0].word.Load())
		return state == cbHalfOpen && probes == 1
	}, time.Second, time.Millisecond, "probe should be admitted and hang")

	gBefore := l.breakers[0].generations.Load()
	time.Sleep(45 * time.Millisecond) // exceed ProbeTimeout

	_, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil)) // saturated + stale -> reclaim -> re-open
	assert.Equal(t, ErrUnavailable, err)
	assert.Equal(t, cbOpen, cbStateOf(l, 0), "hung probe reclaimed; target re-opened")
	assert.Greater(t, l.breakers[0].generations.Load(), gBefore, "reclaim re-opened with a longer backoff")
}

func TestCircuitBreaker_OpenCooldownRateLimitsProbes(t *testing.T) {
	t.Parallel()
	// The OPEN cooldown gates probes even under a contended re-open: a down target
	// admits ~one probe per cooldown, not a flood. A stale-timer TOCTOU (reading an
	// expired deadline right after the word flipped to OPEN) would let probes through
	// with zero cooldown, hammering the backend.
	t0 := &fakeUpstream{}
	t0.down.Store(true)
	l := &CircuitBreakingLoadBalancer{
		Targets: newEjectTargets(t0), FailureThreshold: 1, OpenTimeout: 20 * time.Millisecond, MaxOpenTimeout: 20 * time.Millisecond, HalfOpenMaxProbes: 1,
	}
	driveLB(l, 1) // trip
	hammer(l, 40, 200*time.Millisecond)

	total := t0.calls.Load()
	assert.Less(t, total, int64(40), "cooldown rate-limits probes; no stale-timer flood")
	assert.Greater(t, total, int64(1), "probes are admitted after each cooldown")
}

func TestCircuitBreakerConcurrentMixedHealth(t *testing.T) {
	t.Parallel()
	t0, t1 := &fakeUpstream{}, &fakeUpstream{}
	t0.down.Store(true)
	l := &CircuitBreakingLoadBalancer{Targets: newEjectTargets(t0, t1), FailureThreshold: 3, OpenTimeout: time.Millisecond}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			driveLB(l, 50)
		}()
	}
	wg.Wait()
	assert.Positive(t, t1.calls.Load(), "the healthy target served traffic")
}
