package prom_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet/pkg/upstream"

	. "github.com/moonrhythm/parapet/pkg/prom"
)

func gaugeValue(t *testing.T, name string, want map[string]string) float64 {
	t.Helper()
	mfs, err := Registry().Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, p := range m.GetLabel() {
				got[p.GetName()] = p.GetValue()
			}
			if subset(want, got) {
				return m.GetGauge().GetValue()
			}
		}
	}
	return -1
}

func TestUpstreamState(t *testing.T) {
	observe := UpstreamState()
	require.NotNil(t, observe)

	const host = "prom-state-test.backend"
	observe(upstream.StateChange{Host: host, From: upstream.StateClosed, To: upstream.StateOpen, Reason: upstream.ReasonTrip})
	observe(upstream.StateChange{Host: host, From: upstream.StateOpen, To: upstream.StateHalfOpen, Reason: upstream.ReasonProbe})

	assert.EqualValues(t, 1, counterValue(t, "parapet_upstream_state_transitions_total",
		map[string]string{"host": host, "from": "closed", "to": "open", "reason": "trip"}))
	assert.EqualValues(t, 1, counterValue(t, "parapet_upstream_state_transitions_total",
		map[string]string{"host": host, "from": "open", "to": "half_open", "reason": "probe"}))
	assert.EqualValues(t, 2, gaugeValue(t, "parapet_upstream_breaker_state", map[string]string{"host": host}),
		"gauge reflects the last To (half_open == 2)")
}

func TestUpstreamFastRejects(t *testing.T) {
	observe := Upstream()
	const host = "prom-fastreject-test.backend"

	observe(nil, upstream.RoundTripInfo{Host: host, Err: upstream.ErrUnavailable})
	assert.EqualValues(t, 1, counterValue(t, "parapet_upstream_fast_rejects_total", map[string]string{"host": host}))

	// A normal transport error is not a fast-reject.
	observe(nil, upstream.RoundTripInfo{Host: host, Err: errors.New("dial fail")})
	assert.EqualValues(t, 1, counterValue(t, "parapet_upstream_fast_rejects_total", map[string]string{"host": host}),
		"only ErrUnavailable (shed-before-round-trip) counts")
}

// countDelta treats counterValue's -1 (absent series) as 0 so the positive-count
// assertions are safe to run repeatedly (-count) against the process-global registry.
func countDelta(before, after float64) float64 {
	if before < 0 {
		before = 0
	}
	return after - before
}

func TestUpstreamState_ProbeDownCause(t *testing.T) {
	observe := UpstreamState()
	const host = "prom-probedown-test.backend"
	downLbl := map[string]string{"host": host, "cause": "timeout"}
	transLbl := map[string]string{"host": host, "from": "closed", "to": "open", "reason": "probe_down"}
	baseDown := counterValue(t, "parapet_upstream_probe_down_total", downLbl)
	baseTrans := counterValue(t, "parapet_upstream_state_transitions_total", transLbl)

	observe(upstream.StateChange{
		Host: host, From: upstream.StateClosed, To: upstream.StateOpen,
		Reason: upstream.ReasonProbeDown, Cause: upstream.CauseTimeout,
	})

	// The cause rides on the focused probe-down counter...
	assert.EqualValues(t, 1, countDelta(baseDown, counterValue(t, "parapet_upstream_probe_down_total", downLbl)))
	// ...and the transition still flows into the UNCHANGED authoritative counter.
	assert.EqualValues(t, 1, countDelta(baseTrans, counterValue(t, "parapet_upstream_state_transitions_total", transLbl)))
	assert.EqualValues(t, 1, gaugeValue(t, "parapet_upstream_breaker_state", map[string]string{"host": host}),
		"gauge reflects To=open (1)")
}

func TestUpstreamState_RecoverNoProbeDownSeries(t *testing.T) {
	observe := UpstreamState()
	const host = "prom-proberecover-test.backend"
	transLbl := map[string]string{"host": host, "from": "open", "to": "closed", "reason": "probe_recover"}
	baseTrans := counterValue(t, "parapet_upstream_state_transitions_total", transLbl)

	observe(upstream.StateChange{
		Host: host, From: upstream.StateOpen, To: upstream.StateClosed,
		Reason: upstream.ReasonProbeRecover, // Cause defaults to CauseNone
	})

	assert.EqualValues(t, 1, countDelta(baseTrans, counterValue(t, "parapet_upstream_state_transitions_total", transLbl)))
	assert.EqualValues(t, -1, counterValue(t, "parapet_upstream_probe_down_total", map[string]string{"host": host}),
		"a recover (CauseNone) creates no probe_down series")
	assert.EqualValues(t, 0, gaugeValue(t, "parapet_upstream_breaker_state", map[string]string{"host": host}),
		"gauge reflects To=closed (0)")
}

func TestUpstreamState_EjectDoesNotTouchProbeDown(t *testing.T) {
	observe := UpstreamState()
	const host = "prom-eject-noprobe-test.backend"
	transLbl := map[string]string{"host": host, "from": "closed", "to": "open", "reason": "eject"}
	baseTrans := counterValue(t, "parapet_upstream_state_transitions_total", transLbl)

	observe(upstream.StateChange{
		Host: host, From: upstream.StateClosed, To: upstream.StateOpen, Reason: upstream.ReasonEject,
	})

	assert.EqualValues(t, 1, countDelta(baseTrans, counterValue(t, "parapet_upstream_state_transitions_total", transLbl)))
	assert.EqualValues(t, -1, counterValue(t, "parapet_upstream_probe_down_total", map[string]string{"host": host}),
		"a non-probe emitter never populates the probe_down counter")
}

// Make circuit-breaker / ejection state observable: count transitions and track
// the current state per backend.
func ExampleUpstreamState() {
	lb := upstream.NewCircuitBreakingLoadBalancer([]*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: &upstream.HTTPTransport{}},
		{Host: "10.0.0.2:8080", Transport: &upstream.HTTPTransport{}},
	})
	lb.OnStateChange = UpstreamState() // prom.UpstreamState()

	u := upstream.New(lb)
	u.OnRoundTrip = Upstream() // prom.Upstream() — also counts fast-reject 503s
	_ = u
}
