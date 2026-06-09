package upstream

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatedBalancer is every package balancer: an http.RoundTripper that accepts an
// active-HC gate.
type gatedBalancer interface {
	http.RoundTripper
	setHealthGate(gate []atomic.Bool)
}

func gateTargets(rec http.RoundTripper, hosts ...string) []*Target {
	ts := make([]*Target, len(hosts))
	for i, h := range hosts {
		ts[i] = &Target{Host: h, Transport: rec}
	}
	return ts
}

// gateBuilders is every gateable balancer paired with its documented all-down
// policy (sheds 503 vs fails open). Shared by the gate tests so each of the six is
// exercised by name — must-fix #5: gate ALL balancers, with no silent no-op.
var gateBuilders = []struct {
	name         string
	build        func([]*Target) gatedBalancer
	shedsAllDown bool
}{
	{"RoundRobin", func(ts []*Target) gatedBalancer { return NewRoundRobinLoadBalancer(ts) }, false},
	{"Weighted", func(ts []*Target) gatedBalancer { return NewWeightedRoundRobinLoadBalancer(ts) }, false},
	{"Ejecting", func(ts []*Target) gatedBalancer { return NewEjectingLoadBalancer(ts) }, false},
	{"LatencyEjecting", func(ts []*Target) gatedBalancer { return NewLatencyEjectingLoadBalancer(ts) }, false},
	{"LeastConn", func(ts []*Target) gatedBalancer { return NewLeastConnLoadBalancer(ts) }, false},
	{"CircuitBreaking", func(ts []*Target) gatedBalancer { return NewCircuitBreakingLoadBalancer(ts) }, true},
}

// TestGate_AllDownPolicy locks in each balancer's documented all-down behavior when
// the gate marks every target down: the fail-open balancers route best-effort, the
// circuit breaker sheds 503.
func TestGate_AllDownPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range gateBuilders {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts := gateTargets(freshBody(), "t0", "t1", "t2")
			lb := tc.build(ts)
			gate := make([]atomic.Bool, len(ts)) // all false == every target down
			lb.setHealthGate(gate)

			resp, err := lb.RoundTrip(httptest.NewRequest("GET", "/", nil))
			if tc.shedsAllDown {
				assert.Nil(t, resp)
				assert.ErrorIs(t, err, ErrUnavailable, "the circuit breaker sheds when all targets are gated down")
			} else {
				require.NoError(t, err, "a fail-open balancer routes best-effort when all targets are gated down")
				require.NotNil(t, resp)
				resp.Body.Close()
			}
		})
	}
}

// TestGate_SkipsDownTarget confirms EVERY gateable balancer actually removes a single
// down target from rotation (the common case), routing its share to the survivors —
// run per balancer so a regression to a silent gate no-op is caught (must-fix #5).
func TestGate_SkipsDownTarget(t *testing.T) {
	t.Parallel()
	for _, tc := range gateBuilders {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &recordingTransport{}
			ts := gateTargets(rec, "t0", "t1", "t2")
			lb := tc.build(ts)
			gate := make([]atomic.Bool, 3)
			gate[0].Store(true)
			gate[2].Store(true) // t1 is down; t0 and t2 up

			lb.setHealthGate(gate)
			for range 30 {
				resp, err := lb.RoundTrip(httptest.NewRequest("GET", "/", nil))
				require.NoError(t, err)
				resp.Body.Close()
			}
			c := rec.counts()
			assert.Zero(t, c["t1"], "the down target gets no traffic")
			assert.Equal(t, 30, c["t0"]+c["t2"], "its share routes to the survivors")
		})
	}
}

// TestGate_ConcurrentFlipWhilePicking exercises the shipped interleaving the prober
// actually produces: one goroutine flips the gate while others drive RoundTrip. It
// asserts only that picking under a churning gate never panics and a fail-open
// balancer never spuriously sheds (the real guard is -race cleanliness).
func TestGate_ConcurrentFlipWhilePicking(t *testing.T) {
	t.Parallel()
	for _, tc := range gateBuilders {
		if tc.shedsAllDown {
			continue // a breaker legitimately sheds when the flip lands all-down
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &recordingTransport{}
			ts := gateTargets(rec, "t0", "t1", "t2")
			lb := tc.build(ts)
			gate := make([]atomic.Bool, 3)
			for i := range gate {
				gate[i].Store(true)
			}
			lb.setHealthGate(gate)

			stop := make(chan struct{})
			var flipper sync.WaitGroup
			flipper.Add(1)
			go func() {
				defer flipper.Done()
				for n := 0; ; n++ {
					select {
					case <-stop:
						return
					default:
					}
					gate[n%3].Store(n%2 == 0) // never holds all three down for long
				}
			}()

			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range 200 {
						resp, err := lb.RoundTrip(httptest.NewRequest("GET", "/", nil))
						assert.NoError(t, err, "a fail-open balancer never spuriously sheds under a gate flip")
						if resp != nil && resp.Body != nil {
							resp.Body.Close()
						}
					}
				}()
			}
			wg.Wait()
			close(stop)
			flipper.Wait()
		})
	}
}

// TestLeastConnGate_HealthFailsOpenButCapacitySheds pins the LeastConn split: the
// health gate never sheds on its own (a fully-dark pool fails open), but the bulkhead
// cap still sheds once the (fail-open) targets are saturated.
func TestLeastConnGate_HealthFailsOpenButCapacitySheds(t *testing.T) {
	t.Parallel()
	ts := []*Target{
		{Host: "a", Transport: freshBody(), MaxConcurrent: 1},
		{Host: "b", Transport: freshBody(), MaxConcurrent: 1},
	}
	lb := NewLeastConnLoadBalancer(ts)
	gate := make([]atomic.Bool, 2) // all down
	lb.setHealthGate(gate)

	// All gated down but under cap: fail open and route (don't 503 a whole pool on a
	// possibly-broken probe path). Hold both in-flight to fill their caps.
	r1, err := lb.RoundTrip(httptest.NewRequest("GET", "/", nil))
	require.NoError(t, err, "all-down but under cap fails open")
	r2, err := lb.RoundTrip(httptest.NewRequest("GET", "/", nil))
	require.NoError(t, err)

	// Now down AND saturated: the bulkhead capacity decision wins -> shed.
	resp, err := lb.RoundTrip(httptest.NewRequest("GET", "/", nil))
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrUnavailable, "down and at cap -> shed (the bulkhead contract)")

	r1.Body.Close()
	r2.Body.Close()
}

// TestWeightedGate_SurvivorRatioExact is the headline regression test for the SWRR
// gate (must-fix): with a weight-1 peer gated down, the two survivors must keep their
// exact 3:1 share — the naive "skip but subtract the full total" drifts it (~2.33:1).
func TestWeightedGate_SurvivorRatioExact(t *testing.T) {
	t.Parallel()
	rec := &recordingTransport{}
	ts := []*Target{
		{Host: "heavy", Transport: rec, Weight: 3},
		{Host: "light", Transport: rec, Weight: 1},
		{Host: "down", Transport: rec, Weight: 1},
	}
	lb := NewWeightedRoundRobinLoadBalancer(ts)
	gate := make([]atomic.Bool, 3)
	gate[0].Store(true)
	gate[1].Store(true)
	// gate[2] (down) stays false

	lb.setHealthGate(gate)
	const picks = 4000
	for range picks {
		resp, err := lb.RoundTrip(httptest.NewRequest("GET", "/", nil))
		require.NoError(t, err)
		resp.Body.Close()
	}
	c := rec.counts()
	assert.Zero(t, c["down"], "a gated-down peer is never selected")
	require.Positive(t, c["light"])
	assert.InDelta(t, 3.0, float64(c["heavy"])/float64(c["light"]), 0.05,
		"survivors keep their exact weighted ratio (3:1), not a drifted one")
}

// TestWeightedGate_RecoveryNoStarvation covers the second SWRR must-fix: a peer that
// recovers after a long down period must NOT win every subsequent pick (its stale
// currentWeight is reset), and the pool reconverges to a fair share.
func TestWeightedGate_RecoveryNoStarvation(t *testing.T) {
	t.Parallel()
	rec := &recordingTransport{}
	ts := gateTargets(rec, "a", "b", "c")
	for i := range ts {
		ts[i].Weight = 1
	}
	lb := NewWeightedRoundRobinLoadBalancer(ts)
	gate := make([]atomic.Bool, 3)
	for i := range gate {
		gate[i].Store(true)
	}
	lb.setHealthGate(gate)
	lb.once.Do(lb.init) // build peers so the direct pick() calls below are valid

	gate[2].Store(false) // c goes down
	for range 3000 {     // a,b cycle while c's state is frozen
		lb.pick()
	}
	gate[2].Store(true) // c recovers

	first := map[string]int{}
	for range 30 {
		first[lb.pick().Host]++
	}
	assert.Less(t, first["c"], 30, "a recovered peer does not thunder-reinstate (win every pick)")

	long := map[string]int{}
	for range 3000 {
		long[lb.pick().Host]++
	}
	assert.InDelta(t, 1000, long["a"], 120, "the pool reconverges to a fair share")
	assert.InDelta(t, 1000, long["b"], 120)
	assert.InDelta(t, 1000, long["c"], 120)
}

// TestGate_NilGateNoOp confirms the zero-cost default: with no gate installed every
// balancer behaves exactly as before (here, plain round-robin over all targets).
func TestGate_NilGateNoOp(t *testing.T) {
	t.Parallel()
	rec := &recordingTransport{}
	ts := gateTargets(rec, "t0", "t1", "t2")
	lb := NewRoundRobinLoadBalancer(ts) // no setHealthGate call: gate stays nil
	for range 6 {
		resp, err := lb.RoundTrip(httptest.NewRequest("GET", "/", nil))
		require.NoError(t, err)
		resp.Body.Close()
	}
	assert.Equal(t, []string{"t0", "t1", "t2", "t0", "t1", "t2"}, rec.hits, "nil gate => unchanged round-robin")
}
