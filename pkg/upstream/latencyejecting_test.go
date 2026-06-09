package upstream

import (
	"math"
	"net/http/httptest"
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
		slow := &fakeUpstream{}
		slow.delay.Store(int64(latSlow))
		fast := []*fakeUpstream{slow, {}, {}, {}, {}}
		l := latTestLB(newEjectTargets(fast...))
		driveLB(l, 300)
		assert.True(t, latEjected(l, 0), "the slow target is ejected")
		for i := 1; i < 5; i++ {
			assert.False(t, latEjected(l, i), "fast targets stay in rotation")
		}
	})

	t.Run("NoEjectionOnUniformSlowdown", func(t *testing.T) {
		t.Parallel()
		// THE headline anti-outage-amplifier test: everyone slow by the same amount,
		// the median rises with them, nobody is an outlier.
		var trs []*fakeUpstream
		for range 5 {
			u := &fakeUpstream{}
			u.delay.Store(int64(latMild))
			trs = append(trs, u)
		}
		l := latTestLB(newEjectTargets(trs...))
		driveLB(l, 300)
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
		// One clear outlier among otherwise-equal targets; removing it must not make a
		// peer the new outlier (the median is robust).
		slow := &fakeUpstream{}
		slow.delay.Store(int64(latSlow))
		l := latTestLB(newEjectTargets(slow, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
		driveLB(l, 300)
		require.True(t, latEjected(l, 0))
		driveLB(l, 300)
		for i := 1; i < 6; i++ {
			assert.False(t, latEjected(l, i), "no cascade onto the next-slowest")
		}
	})

	t.Run("RecoversAfterCooldownNoImmediateReEject", func(t *testing.T) {
		t.Parallel()
		// A VERY slow host (the critique's flapping case): it must not re-eject on its
		// first post-cooldown sample once it has recovered — eject() reseeds the EWMA.
		slow := &fakeUpstream{}
		slow.delay.Store(int64(20 * time.Millisecond)) // ~10x the others
		l := latTestLB(newEjectTargets(slow, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
		driveLB(l, 200)
		require.True(t, latEjected(l, 0), "very-slow host is ejected")

		slow.delay.Store(0) // recovered
		time.Sleep(40 * time.Millisecond) // past the 30ms cooldown
		base := slow.calls.Load()
		driveLB(l, 200) // re-probe + accumulate fresh fast samples

		assert.False(t, latEjected(l, 0), "a recovered host is not re-ejected on stale data")
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
	slow.delay.Store(int64(latSlow))
	l := latTestLB(newEjectTargets(slow, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
	hammer(l, 16, 200*time.Millisecond)
	assert.True(t, latEjected(l, 0), "the slow target ends ejected under concurrency")
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
