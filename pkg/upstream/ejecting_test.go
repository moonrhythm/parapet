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
)

// fakeUpstream is a transport whose health can be toggled at runtime.
type fakeUpstream struct {
	calls  atomic.Int64
	down   atomic.Bool
	delay  atomic.Int64 // response delay in nanos (for latency tests); 0 = none
	status int          // response status when up; 0 means 200
}

func (t *fakeUpstream) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls.Add(1)
	if t.down.Load() {
		return nil, errors.New("dial tcp: connection refused")
	}
	if d := t.delay.Load(); d > 0 {
		time.Sleep(time.Duration(d))
	}
	w := httptest.NewRecorder()
	if t.status != 0 {
		w.WriteHeader(t.status)
	}
	return w.Result(), nil
}

func newEjectTargets(trs ...*fakeUpstream) []*Target {
	targets := make([]*Target, len(trs))
	for i, tr := range trs {
		targets[i] = &Target{Host: fmt.Sprintf("upstream%d", i), Transport: tr}
	}
	return targets
}

func drive(l *EjectingLoadBalancer, n int) {
	for range n {
		r := httptest.NewRequest("GET", "/", nil)
		resp, _ := l.RoundTrip(r)
		if resp != nil {
			resp.Body.Close()
		}
	}
}

func TestEjectingLoadBalancer(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		l := NewEjectingLoadBalancer(nil)
		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Nil(t, resp)
		assert.Equal(t, ErrUnavailable, err)
	})

	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()
		l := NewEjectingLoadBalancer(newEjectTargets(&fakeUpstream{}))
		drive(l, 1)
		assert.Equal(t, defaultMaxFails, l.MaxFails)
		assert.Equal(t, defaultEjectTimeout, l.EjectTimeout)
		assert.Equal(t, defaultMaxEjectTimeout, l.MaxEjectTimeout)
	})

	t.Run("RoundRobinWhenHealthy", func(t *testing.T) {
		t.Parallel()
		t0, t1 := &fakeUpstream{}, &fakeUpstream{}
		l := NewEjectingLoadBalancer(newEjectTargets(t0, t1))
		drive(l, 10)
		assert.Equal(t, int64(5), t0.calls.Load())
		assert.Equal(t, int64(5), t1.calls.Load())
	})

	t.Run("EjectsAfterMaxFails", func(t *testing.T) {
		t.Parallel()
		t0, t1 := &fakeUpstream{}, &fakeUpstream{}
		t0.down.Store(true)
		l := &EjectingLoadBalancer{Targets: newEjectTargets(t0, t1), MaxFails: 3}

		// 6 round-robin calls hit t0 three times, ejecting it.
		drive(l, 6)
		assert.Equal(t, int64(3), t0.calls.Load())

		// once ejected, t0 is skipped and all traffic goes to t1.
		before := t0.calls.Load()
		drive(l, 10)
		assert.Equal(t, before, t0.calls.Load(), "ejected target must receive no traffic")
		assert.Equal(t, int64(13), t1.calls.Load())
	})

	t.Run("RecoversAfterCooldown", func(t *testing.T) {
		t.Parallel()
		t0, t1 := &fakeUpstream{}, &fakeUpstream{}
		t0.down.Store(true)
		l := &EjectingLoadBalancer{
			Targets:      newEjectTargets(t0, t1),
			MaxFails:     3,
			EjectTimeout: 20 * time.Millisecond,
		}

		drive(l, 6) // eject t0
		ejected := t0.calls.Load()
		drive(l, 4)
		assert.Equal(t, ejected, t0.calls.Load(), "still ejected")

		t0.down.Store(false)
		time.Sleep(40 * time.Millisecond) // let the cooldown expire
		drive(l, 6)
		assert.Greater(t, t0.calls.Load(), ejected, "target back in rotation after cooldown")
	})

	t.Run("SuccessResetsFailures", func(t *testing.T) {
		t.Parallel()
		t0 := &fakeUpstream{}
		t0.down.Store(true)
		l := &EjectingLoadBalancer{Targets: newEjectTargets(t0), MaxFails: 3}

		drive(l, 2) // two failures, not yet ejected
		t0.down.Store(false)
		drive(l, 1) // success resets the counter
		t0.down.Store(true)
		drive(l, 2) // two more failures: still below threshold

		assert.Equal(t, int64(0), l.targets[0].ejectedUntil.Load(), "should not be ejected")
		assert.Equal(t, int32(2), l.targets[0].fails.Load())
	})

	t.Run("FailsOpenWhenAllEjected", func(t *testing.T) {
		t.Parallel()
		t0, t1 := &fakeUpstream{}, &fakeUpstream{}
		t0.down.Store(true)
		t1.down.Store(true)
		l := &EjectingLoadBalancer{Targets: newEjectTargets(t0, t1), MaxFails: 2}

		drive(l, 4) // eject both
		c0, c1 := t0.calls.Load(), t1.calls.Load()

		// with everything ejected the balancer must still route, not 503.
		drive(l, 4)
		assert.Greater(t, t0.calls.Load()+t1.calls.Load(), c0+c1, "must keep routing when all ejected")
	})

	t.Run("IgnoresClientCancel", func(t *testing.T) {
		t.Parallel()
		l := &EjectingLoadBalancer{
			Targets:  newEjectTargets(&fakeUpstream{}),
			MaxFails: 3,
			IsFailure: func(_ *http.Response, err error) bool {
				// emulate default behavior
				return err != nil && !errors.Is(err, context.Canceled)
			},
		}
		l.once.Do(l.init)
		for range 10 {
			l.record(l.targets[0], nil, fmt.Errorf("canceled: %w", context.Canceled))
		}
		assert.Equal(t, int32(0), l.targets[0].fails.Load())
		assert.Equal(t, int64(0), l.targets[0].ejectedUntil.Load())
	})

	t.Run("IsFailureHookCountsStatus", func(t *testing.T) {
		t.Parallel()
		t0, t1 := &fakeUpstream{status: 500}, &fakeUpstream{}
		l := &EjectingLoadBalancer{
			Targets:  newEjectTargets(t0, t1),
			MaxFails: 3,
			IsFailure: func(resp *http.Response, err error) bool {
				return err != nil || (resp != nil && resp.StatusCode >= 500)
			},
		}
		drive(l, 6) // t0 returns 500 three times -> ejected
		before := t0.calls.Load()
		drive(l, 8)
		assert.Equal(t, before, t0.calls.Load(), "5xx target ejected via IsFailure hook")
	})

	t.Run("ConcurrentBurstIsOneEjection", func(t *testing.T) {
		t.Parallel()
		// A burst of simultaneous in-flight failures all cross the threshold; they
		// must collapse to a single ejection, not inflate the backoff exponent.
		t0 := &fakeUpstream{}
		t0.down.Store(true)
		l := &EjectingLoadBalancer{Targets: newEjectTargets(t0), MaxFails: 3}
		l.once.Do(l.init)
		et := l.targets[0]

		var wg sync.WaitGroup
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				l.record(et, nil, errors.New("connection refused"))
			}()
		}
		wg.Wait()

		assert.Positive(t, et.ejectedUntil.Load(), "target is ejected")
		assert.Equal(t, int32(1), et.ejections.Load(), "a concurrent failure burst counts as exactly one ejection")
	})

	t.Run("RepeatEjectionGrowsBackoffPerWindow", func(t *testing.T) {
		t.Parallel()
		l := &EjectingLoadBalancer{Targets: newEjectTargets(&fakeUpstream{}), MaxFails: 1, EjectTimeout: time.Hour}
		l.once.Do(l.init)
		et := l.targets[0]

		l.eject(et)
		assert.Equal(t, int32(1), et.ejections.Load())

		l.eject(et) // still within the window
		assert.Equal(t, int32(1), et.ejections.Load(), "re-ejecting within the cooldown does not bump the backoff")

		et.ejectedUntil.Store(0) // simulate the cooldown expiring
		l.eject(et)
		assert.Equal(t, int32(2), et.ejections.Load(), "a fresh ejection after cooldown bumps the backoff")
	})
}

func TestEjectingLoadBalancerConcurrent(t *testing.T) {
	t.Parallel()
	t0, t1 := &fakeUpstream{}, &fakeUpstream{}
	t0.down.Store(true)
	l := &EjectingLoadBalancer{Targets: newEjectTargets(t0, t1), MaxFails: 3, EjectTimeout: time.Millisecond}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			drive(l, 50)
		}()
	}
	wg.Wait()
	// t1 is always healthy, so it must have served traffic.
	assert.Positive(t, t1.calls.Load())
}

func TestEjectionTimeoutBackoff(t *testing.T) {
	t.Parallel()
	l := &EjectingLoadBalancer{EjectTimeout: time.Second, MaxEjectTimeout: 5 * time.Second}
	l.once.Do(l.init)
	assert.Equal(t, time.Second, l.ejectionTimeout(1))
	assert.Equal(t, 2*time.Second, l.ejectionTimeout(2))
	assert.Equal(t, 4*time.Second, l.ejectionTimeout(3))
	assert.Equal(t, 5*time.Second, l.ejectionTimeout(4), "capped at MaxEjectTimeout")
	assert.Equal(t, 5*time.Second, l.ejectionTimeout(100), "no overflow on large counts")
}
