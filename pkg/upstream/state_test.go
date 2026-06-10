package upstream

import (
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "closed", StateClosed.String())
	assert.Equal(t, "open", StateOpen.String())
	assert.Equal(t, "half_open", StateHalfOpen.String())
}

func TestReasonString(t *testing.T) {
	t.Parallel()
	for r, s := range map[Reason]string{
		ReasonTrip:    "trip",
		ReasonReopen:  "reopen",
		ReasonHeal:    "heal",
		ReasonProbe:   "probe",
		ReasonExpire:  "expire",
		ReasonEject:   "eject",
		ReasonRecover: "recover",
	} {
		assert.Equal(t, s, r.String())
	}
}

// stateRecorder captures StateChange events for assertions.
type stateRecorder struct {
	mu      sync.Mutex
	changes []StateChange
}

func (r *stateRecorder) fn() StateChangeFunc {
	return func(c StateChange) {
		r.mu.Lock()
		r.changes = append(r.changes, c)
		r.mu.Unlock()
	}
}

func (r *stateRecorder) all() []StateChange {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]StateChange(nil), r.changes...)
}

func (r *stateRecorder) reasons() []Reason {
	out := make([]Reason, 0)
	for _, c := range r.all() {
		out = append(out, c.Reason)
	}
	return out
}

func (r *stateRecorder) count(reason Reason) (n int) {
	for _, c := range r.all() {
		if c.Reason == reason {
			n++
		}
	}
	return
}

func TestCircuitBreaker_StateChangeLifecycle(t *testing.T) {
	t.Parallel()
	// Strictly serial driving so emit order == commit order (it is NOT guaranteed
	// under concurrency — see the burst test, which asserts counts only).
	var rec stateRecorder
	t0 := &fakeUpstream{}
	t0.down.Store(true)
	l := &CircuitBreakingLoadBalancer{
		Targets: newEjectTargets(t0), FailureThreshold: 3, SuccessThreshold: 2, OpenTimeout: 20 * time.Millisecond,
		OnStateChange: rec.fn(),
	}
	driveLB(l, 3) // closed -> open (trip)
	t0.down.Store(false)
	time.Sleep(40 * time.Millisecond) // cooldown expires
	driveLB(l, 1)                     // open -> half_open (probe), sub-threshold success
	driveLB(l, 1)                     // half_open -> closed (heal)

	assert.Equal(t, []Reason{ReasonTrip, ReasonProbe, ReasonHeal}, rec.reasons())
	first := rec.all()[0]
	assert.Equal(t, StateClosed, first.From)
	assert.Equal(t, StateOpen, first.To)
}

func TestCircuitBreaker_StateChangeConcurrentBurstOneTrip(t *testing.T) {
	t.Parallel()
	var rec stateRecorder
	l := &CircuitBreakingLoadBalancer{Targets: newEjectTargets(&fakeUpstream{}), FailureThreshold: 1, OnStateChange: rec.fn()}
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
	assert.Equal(t, 1, rec.count(ReasonTrip), "a concurrent failure burst emits exactly one trip")
}

func TestEjectingLoadBalancer_StateChange(t *testing.T) {
	t.Parallel()
	var rec stateRecorder
	t0 := &fakeUpstream{}
	t0.down.Store(true)
	l := &EjectingLoadBalancer{Targets: newEjectTargets(t0), MaxFails: 3, EjectTimeout: 20 * time.Millisecond, OnStateChange: rec.fn()}
	driveLB(l, 3) // eject
	require.Equal(t, 1, rec.count(ReasonEject))
	c0 := rec.all()[0]
	assert.Equal(t, StateClosed, c0.From)
	assert.Equal(t, StateOpen, c0.To)

	t0.down.Store(false)
	time.Sleep(40 * time.Millisecond)
	driveLB(l, 5) // re-admitted; first success recovers
	assert.Equal(t, 1, rec.count(ReasonRecover), "a confirmed success emits exactly one recover")
}

func TestEjectingLoadBalancer_StateChangeConcurrentOneEject(t *testing.T) {
	t.Parallel()
	var rec stateRecorder
	t0 := &fakeUpstream{}
	t0.down.Store(true)
	l := &EjectingLoadBalancer{Targets: newEjectTargets(t0), MaxFails: 1, OnStateChange: rec.fn()}
	l.once.Do(l.init)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.record(l.targets[0], nil, errors.New("down"))
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, rec.count(ReasonEject), "a concurrent failure burst emits exactly one eject")
}

func TestLatencyEjectingLoadBalancer_StateChange(t *testing.T) {
	t.Parallel()
	var rec stateRecorder
	l := latTestLB(newEjectTargets(&fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}, &fakeUpstream{}))
	l.OnStateChange = rec.fn()
	l.once.Do(l.init)
	// Synthetic constant durations through record(): timing real round-trips lets
	// scheduler stalls inflate the fast peers' EWMA seeds (lifting the median so the
	// 5ms outlier never ejects and the count stays 0). Constant samples pin each EWMA,
	// so the whole record -> poolMedian -> guard-rails -> eject -> OnStateChange path
	// runs deterministically. Fast peers first each round so the slow peer's
	// MinSamples-th record sees a valid >=MinHosts baseline and ejects exactly once.
	resp := httptest.NewRecorder().Result()
	for range int(l.MinSamples) {
		for p := 1; p < 4; p++ {
			l.record(&l.peers[p], 100*time.Microsecond, resp, nil)
		}
		l.record(&l.peers[0], latSlow, resp, nil)
	}
	assert.Equal(t, 1, rec.count(ReasonEject), "the slow target's ejection emits ReasonEject")
	for _, c := range rec.all() {
		if c.Reason == ReasonEject {
			assert.Equal(t, StateClosed, c.From, "first ejection comes from closed")
			assert.Equal(t, StateOpen, c.To)
		}
	}
}

func TestEjectingLoadBalancer_StateChangeConcurrentOneRecover(t *testing.T) {
	t.Parallel()
	var rec stateRecorder
	l := &EjectingLoadBalancer{Targets: newEjectTargets(&fakeUpstream{}), MaxFails: 1, OnStateChange: rec.fn()}
	l.once.Do(l.init)
	tt := l.targets[0]
	l.eject(tt) // ejectedUntil future, ejections=1
	require.Equal(t, 1, rec.count(ReasonEject))

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.record(tt, nil, nil) // success -> exactly one goroutine wins the Swap and recovers
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, rec.count(ReasonRecover), "concurrent successes emit exactly one recover")
}
