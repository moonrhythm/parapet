package upstream

import (
	"math"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// latTestLB builds a latency balancer with small, fast-to-exercise thresholds.
func latTestLB(targets []*Target) *LatencyEjectingLoadBalancer {
	return &LatencyEjectingLoadBalancer{
		Targets:       targets,
		MinSamples:    20,
		MinEjectDelta: time.Millisecond,
		HalfLife:      time.Second,
		EjectTimeout:  30 * time.Millisecond,
	}
}

func latEjected(l *LatencyEjectingLoadBalancer, i int) bool {
	return l.peers[i].ejectedUntil.Load() > time.Now().UnixNano()
}

func latEjectedCount(l *LatencyEjectingLoadBalancer) (c int) {
	for i := range l.peers {
		if latEjected(l, i) {
			c++
		}
	}
	return
}

const (
	latSlow = 5 * time.Millisecond
	latMild = 2 * time.Millisecond
)

func TestLatencyEjectingLoadBalancer(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		l := NewLatencyEjectingLoadBalancer(nil)
		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Nil(t, resp)
		assert.Equal(t, ErrUnavailable, err)
	})

	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()
		l := NewLatencyEjectingLoadBalancer(newEjectTargets(&fakeUpstream{}))
		driveLB(l, 1)
		assert.Equal(t, 3.0, l.EjectionFactor)
		assert.EqualValues(t, 100, l.MinSamples)
		assert.Equal(t, 3, l.MinHosts)
		assert.Equal(t, 10*time.Second, l.HalfLife)
		assert.Equal(t, 50*time.Millisecond, l.MinEjectDelta)
		assert.Equal(t, 30, l.MaxEjectionPercent)
		assert.Equal(t, 50, l.PanicThreshold)
		assert.Equal(t, 30*time.Second, l.EjectTimeout)
		assert.Equal(t, 5*time.Minute, l.MaxEjectTimeout)
	})

	t.Run("PanicThresholdClampedAboveCap", func(t *testing.T) {
		t.Parallel()
		l := &LatencyEjectingLoadBalancer{Targets: newEjectTargets(&fakeUpstream{}), PanicThreshold: 20, MaxEjectionPercent: 30}
		driveLB(l, 1)
		assert.Equal(t, 31, l.PanicThreshold, "panic threshold raised above the cap so it is never dead code")
	})

	t.Run("DetectsAndEjectsTheSlowOne", func(t *testing.T) {
		t.Parallel()
		l := latTestLB(newEjectTargets(&fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
		// Sticky cooldown: the slow target need only cross the ejection bar ONCE during
		// the drive and then stays ejected, so the assert cannot race a re-admission /
		// re-probe (which resets samples) when -race stretches the synchronous drive.
		l.EjectTimeout = time.Minute
		l.once.Do(l.init)
		// Feed deterministic synthetic durations through record() instead of timing real
		// round-trips: observe() seeds the EWMA exactly from the FIRST sample, so a
		// single >=1.2ms scheduler stall inside a fast peer's first ~µs measured request
		// would seed it above the ~1ms bar and steal the single MaxEjectionPercent slot
		// from the slow target before its first eligible record. Constant samples pin
		// each EWMA at exactly that constant for any decay weight, making the verdict
		// wall-clock independent while still exercising the MinSamples/MinHosts
		// eligibility-ordering, poolMedian, and cap paths. (The RoundTrip-times-latency
		// wiring stays covered by TestLatencyEjectingConcurrent.)
		resp := httptest.NewRecorder().Result()
		for range 60 {
			l.record(&l.peers[0], latSlow, resp, nil) // slow peer first, mirroring pick order
			for i := 1; i < 5; i++ {
				l.record(&l.peers[i], 100*time.Microsecond, resp, nil)
			}
		}
		assert.True(t, latEjected(l, 0), "the slow target is ejected")
		for i := 1; i < 5; i++ {
			assert.False(t, latEjected(l, i), "fast targets stay in rotation")
		}
	})

	t.Run("NoEjectionOnUniformSlowdown", func(t *testing.T) {
		t.Parallel()
		// THE headline anti-outage-amplifier test: everyone slow by the same amount,
		// the median rises with them, nobody is an outlier.
		l := latTestLB(newEjectTargets(&fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
		l.once.Do(l.init)
		// Synthetic constant durations through record(): when timing real round-trips, a
		// single ~100ms stall inside one peer's measured section near the END of the
		// drive lifts that EWMA past 3x median and the 30ms cooldown outlives the test.
		// Identical synthetic samples pin every EWMA at exactly latMild (a convex
		// combination of equal values), so no stall can manufacture an outlier.
		// (Deliberately NOT sticky: keep latTestLB's EjectTimeout as-is.)
		resp := httptest.NewRecorder().Result()
		for i := range 300 {
			l.record(&l.peers[i%5], latMild, resp, nil)
		}
		assert.Zero(t, latEjectedCount(l), "a uniform slowdown ejects no one")
	})

	t.Run("ColdStartNoEject", func(t *testing.T) {
		t.Parallel()
		slow := &fakeUpstream{}
		slow.delay.Store(int64(latSlow))
		l := latTestLB(newEjectTargets(slow, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
		l.MinSamples = 100
		driveLB(l, 40) // < MinSamples per target
		assert.Zero(t, latEjectedCount(l), "no ejection before MinSamples")
	})

	t.Run("TwoNodePoolNeverLatencyEjects", func(t *testing.T) {
		t.Parallel()
		slow := &fakeUpstream{}
		slow.delay.Store(int64(latSlow))
		l := latTestLB(newEjectTargets(slow, &fakeUpstream{}))
		driveLB(l, 200)
		assert.Zero(t, latEjectedCount(l), "a pool below MinHosts cannot name an outlier")
	})

	t.Run("ErrorsAreNotTimedNorLatencyEjected", func(t *testing.T) {
		t.Parallel()
		dead := &fakeUpstream{}
		dead.down.Store(true)
		l := latTestLB(newEjectTargets(dead, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
		driveLB(l, 200)
		assert.Zero(t, l.peers[0].samples.Load(), "a transport error never feeds the latency EWMA")
		assert.False(t, latEjected(l, 0), "this balancer does not error-eject (use the breaker for that)")
	})

	t.Run("CapBindsAtFloor", func(t *testing.T) {
		t.Parallel()
		// n=6, MaxEjectionPercent=30 -> at most floor(6*0.30)=1 ejected even with two outliers.
		s0, s1 := &fakeUpstream{}, &fakeUpstream{}
		s0.delay.Store(int64(latSlow))
		s1.delay.Store(int64(latSlow + time.Millisecond))
		l := latTestLB(newEjectTargets(s0, s1, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
		l.MaxEjectionPercent = 30
		driveLB(l, 400)
		assert.LessOrEqual(t, latEjectedCount(l), 1, "the cap blocks the second outlier (no pool drain)")
	})

	t.Run("NoOscillationCascade", func(t *testing.T) {
		t.Parallel()
		// One clear outlier among otherwise-similar targets; removing it must not make a
		// peer the new outlier (the median is robust).
		l := latTestLB(newEjectTargets(&fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
		l.EjectTimeout = time.Minute // sticky (see DetectsAndEjectsTheSlowOne)
		l.once.Do(l.init)
		// Synthetic durations through record() — same first-sample-poison cap-steal race
		// as DetectsAndEjectsTheSlowOne (cap = floor(6*0.3) = 1 slot a poisoned fast peer
		// could consume forever). The fast peers get a small deterministic SPREAD, not
		// identical latencies, so poolMedian sorts genuinely unequal survivor baselines
		// in phase 2. (The spread alone cannot flag a non-robust baseline statistic:
		// MinEjectDelta=1ms floors the bar above every ~100-200µs survivor regardless,
		// and the sticky-ejected peer 0 holds the single cap slot.)
		resp := httptest.NewRecorder().Result()
		fastLat := func(i int) time.Duration { return 100*time.Microsecond + time.Duration(i)*20*time.Microsecond }
		for range 60 {
			l.record(&l.peers[0], latSlow, resp, nil)
			for i := 1; i < 6; i++ {
				l.record(&l.peers[i], fastLat(i), resp, nil)
			}
		}
		require.True(t, latEjected(l, 0))
		// Phase 2: the ejected peer receives no traffic (pick skips it); the survivors
		// keep their spread of baselines.
		for range 60 {
			for i := 1; i < 6; i++ {
				l.record(&l.peers[i], fastLat(i), resp, nil)
			}
		}
		for i := 1; i < 6; i++ {
			assert.False(t, latEjected(l, i), "no cascade onto the next-slowest")
		}
	})

	t.Run("RecoversAfterCooldownNoImmediateReEject", func(t *testing.T) {
		t.Parallel()
		// A VERY slow host (the critique's flapping case): it must not re-eject on its
		// first post-cooldown samples once it has recovered — eject() reseeds the EWMA.
		// Wall-clock races are designed out: the cooldown is "expired" by rewinding
		// ejectedUntil (not by sleeping past a real 30ms window the drive can outlast),
		// and both phases feed deterministic synthetic durations through record() —
		// timing real 2ms sleeps lets contention inflate the fast peers' sleep-wake
		// latency multiplicatively, collapsing the 20ms-vs-2ms ratio below the 3x bar
		// so the host never ejects at all. The peers' baseline stays at latMild (not
		// ~0µs) so the recovered host sits well under a bar a stale 20ms EWMA (the bug
		// this guards against) would still trip.
		slow := &fakeUpstream{}
		peers := []*fakeUpstream{slow, {}, {}, {}}
		l := latTestLB(newEjectTargets(peers...))
		l.EjectTimeout = time.Minute // sticky (see DetectsAndEjectsTheSlowOne)
		rec := &stateRecorder{}
		l.OnStateChange = rec.fn()
		l.once.Do(l.init)
		resp := httptest.NewRecorder().Result()
		for range 50 {
			for i := 1; i < 4; i++ {
				l.record(&l.peers[i], latMild, resp, nil)
			}
			// Mirror pick() semantics: an ejected peer receives no traffic, so its
			// stale samples/EWMA must not keep rebuilding after the eject — feeding it
			// anyway would silently re-arm a phase-2 re-eject and the test would then
			// pass only via the later heal, not the no-flap property in its name.
			if !latEjected(l, 0) {
				l.record(&l.peers[0], 20*time.Millisecond, resp, nil) // ~10x the others
			}
		}
		require.True(t, latEjected(l, 0), "very-slow host is ejected")

		l.peers[0].ejectedUntil.Store(time.Now().UnixNano() - 1) // cooldown elapsed, deterministically
		for range 50 {                                           // recovered to peer level: fresh post-cooldown samples
			for i := range 4 {
				l.record(&l.peers[i], latMild, resp, nil)
			}
		}
		assert.False(t, latEjected(l, 0), "a recovered host is not re-ejected on stale data")
		// Pin the no-flap property itself: exactly one eject episode, one recovery —
		// a flap-then-heal implementation cannot sneak past the final state assert.
		assert.Equal(t, 1, rec.count(ReasonEject), "no second eject on stale data")
		assert.Equal(t, 1, rec.count(ReasonRecover), "exactly one recovery")

		// And pick() must actually re-admit it: an expired deadline puts it back in
		// round-robin rotation (real round-trips; ejection already asserted above).
		base := slow.calls.Load()
		driveLB(l, 8)
		assert.Greater(t, slow.calls.Load(), base, "the recovered host serves traffic again")
	})

	t.Run("FailOpenAllEjected", func(t *testing.T) {
		t.Parallel()
		l := latTestLB(newEjectTargets(&fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
		l.once.Do(l.init)
		future := time.Now().Add(time.Hour).UnixNano()
		for i := range l.peers {
			l.peers[i].ejectedUntil.Store(future) // force every target ejected
		}
		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		require.NoError(t, err, "fails open, never ErrUnavailable for a non-empty pool")
		require.NotNil(t, resp)
		resp.Body.Close()
	})

	t.Run("PanicModeRoutesToAll", func(t *testing.T) {
		t.Parallel()
		// >PanicThreshold% ejected -> pick routes to ALL targets, including ejected ones
		// (a latency-ejected host is slow, not dead, so it is a usable fallback).
		trs := []*fakeUpstream{{}, {}, {}, {}}
		l := latTestLB(newEjectTargets(trs...))
		l.once.Do(l.init)
		future := time.Now().Add(time.Hour).UnixNano()
		l.peers[0].ejectedUntil.Store(future)
		l.peers[1].ejectedUntil.Store(future)
		l.peers[2].ejectedUntil.Store(future) // 3/4 = 75% > 50% panic
		driveLB(l, 80)
		for i := range trs {
			assert.Positive(t, trs[i].calls.Load(), "panic mode routes to every target, ejected or not")
		}
	})
}

func TestLatencyEjectingBackoff(t *testing.T) {
	t.Parallel()
	l := &LatencyEjectingLoadBalancer{EjectTimeout: time.Second, MaxEjectTimeout: 5 * time.Second}
	l.once.Do(l.init)
	assert.Equal(t, time.Second, l.ejectionTimeout(1))
	assert.Equal(t, 2*time.Second, l.ejectionTimeout(2))
	assert.Equal(t, 4*time.Second, l.ejectionTimeout(3))
	assert.Equal(t, 5*time.Second, l.ejectionTimeout(4), "capped at MaxEjectTimeout")
	assert.Equal(t, 5*time.Second, l.ejectionTimeout(100), "no overflow on large counts")
}

func TestLatencyPoolMedian(t *testing.T) {
	t.Parallel()
	l := &LatencyEjectingLoadBalancer{Targets: newEjectTargets(&fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{})}
	l.once.Do(l.init)
	l.MinSamples = 1
	set := func(i int, ms float64, samples uint64) {
		l.peers[i].samples.Store(samples)
		if ms > 0 {
			l.peers[i].ewmaBits.Store(math.Float64bits(ms * float64(time.Millisecond)))
		}
	}
	// 3 eligible (odd) -> middle.
	set(0, 1, 5)
	set(1, 2, 5)
	set(2, 9, 5)
	set(3, 1, 0) // under-sampled -> excluded
	set(4, 0, 5) // unseeded -> excluded
	med, elig := l.poolMedian()
	assert.Equal(t, 3, elig)
	assert.InDelta(t, 2*float64(time.Millisecond), med, 1, "odd median = middle value")

	// 4 eligible (even) -> mean of the two middles.
	set(3, 3, 5)
	med, elig = l.poolMedian()
	assert.Equal(t, 4, elig)
	assert.InDelta(t, 2.5*float64(time.Millisecond), med, 1, "even median = mean of two middles")
}

func TestLatencyEjectingConcurrent(t *testing.T) {
	t.Parallel()
	slow := &fakeUpstream{}
	// A clear outlier so the eject path (almost always) runs under the hammer too.
	slow.delay.Store(int64(30 * time.Millisecond))
	l := latTestLB(newEjectTargets(slow, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
	l.MinSamples = 10
	l.EjectTimeout = time.Minute // sticky: once ejected it stays, so the poll never races a re-admit
	l.once.Do(l.init)            // build peers now so the poll goroutine never races lazy init

	// This test exists for -race coverage of concurrent pick/observe/eject and for
	// the RoundTrip-measures-and-records wiring. It does NOT assert the ejection
	// verdict: under heavy co-test contention every MEASURED latency inflates by
	// scheduler stall, so the slow:median ratio can sit under EjectionFactor for the
	// whole deadline and an "eventually ejected" assert flakes (observed under a
	// 6-package -race stress). The verdict is asserted deterministically with
	// synthetic samples in DetectsAndEjectsTheSlowOne; here we assert only facts
	// that hold at any execution speed.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
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
	require.Eventually(t, func() bool {
		for i := 1; i < len(l.peers); i++ {
			// A "fast" peer whose first measured sample caught a scheduler stall can
			// itself be ejected (seed poison) — eject() zeroes its EWMA and, sticky,
			// it never records again. ejectedUntil != 0 proves the full
			// record->eject pipeline ran for it, which is the wiring fact we want.
			if l.peers[i].ewmaBits.Load() == 0 && l.peers[i].ejectedUntil.Load() == 0 {
				return false // this peer's measured latency never reached its EWMA
			}
		}
		// The slow peer was sampled too — or was already ejected, which resets its
		// counts (and proves the whole record->eject pipeline ran end to end).
		return latEjected(l, 0) || l.peers[0].samples.Load() > 0
	}, 3*time.Second, 10*time.Millisecond,
		"every peer's measured latency reaches its EWMA under concurrency")
	close(stop)
	wg.Wait()
}

func TestLatencyEjectingPickZeroAlloc(t *testing.T) {
	// not parallel: testing.AllocsPerRun must not run concurrently with other tests
	l := latTestLB(newEjectTargets(&fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
	l.once.Do(l.init)
	l.peers[1].ejectedUntil.Store(time.Now().Add(time.Hour).UnixNano()) // partially-ejected pool
	n := len(l.peers)
	allocs := testing.AllocsPerRun(200, func() { l.pick(n) })
	assert.Zero(t, allocs, "pick (incl. the panic-check scan) allocates nothing")

	// poolMedian runs per-record; its stack buffer must keep it zero-alloc for a
	// small pool (the reason it can run off the send path cheaply).
	for i := range l.peers {
		l.peers[i].samples.Store(l.MinSamples)
		l.peers[i].ewmaBits.Store(math.Float64bits(float64(i+1) * float64(time.Millisecond)))
	}
	medAllocs := testing.AllocsPerRun(200, func() { l.poolMedian() })
	assert.Zero(t, medAllocs, "poolMedian uses a stack buffer for n<=16")
}
