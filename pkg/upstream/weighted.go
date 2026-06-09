package upstream

import (
	"net/http"
	"sync"
	"sync/atomic"
)

// NewWeightedRoundRobinLoadBalancer creates a weighted round-robin load balancer
// using smooth weighted round-robin (SWRR). Each target receives a share of
// requests proportional to its Weight; targets with equal weight are served in
// plain round-robin order. Configuration fields are read once, before the first
// request; set them before serving.
func NewWeightedRoundRobinLoadBalancer(targets []*Target) *WeightedRoundRobinLoadBalancer {
	return &WeightedRoundRobinLoadBalancer{Targets: targets}
}

// WeightedRoundRobinLoadBalancer distributes requests across targets in
// proportion to their Weight, using smooth weighted round-robin (the nginx
// algorithm): a heavy target's picks are interleaved with the others rather than
// dealt in a burst, and the long-run share is exactly Weight/sum(Weight). With
// all weights equal it degenerates to plain index-order round-robin.
//
// It balances by request COUNT (use LeastConnLoadBalancer to balance by
// concurrent in-flight requests instead).
//
//nolint:govet // fields grouped by role (state, then config) for readability
type WeightedRoundRobinLoadBalancer struct {
	once  sync.Once
	mu    sync.Mutex
	total int64
	peers []swrrPeer
	gate  []atomic.Bool // active-HC gate; nil = all up (installed before serving)

	// Targets is the set of upstreams to balance across.
	Targets []*Target
}

// swrrPeer holds one target's smooth-weighted-round-robin state.
type swrrPeer struct {
	target  *Target
	weight  int64 // effective weight, >= 1; immutable after init
	current int64 // running currentWeight, guarded by the balancer's mu
	wasUp   bool  // active-HC verdict at the last pick, to detect a down->up recovery
}

func (l *WeightedRoundRobinLoadBalancer) init() {
	l.peers = make([]swrrPeer, len(l.Targets))
	for i, t := range l.Targets {
		w := effectiveWeight(t)
		l.peers[i] = swrrPeer{target: t, weight: w}
		l.total += w // int64; realistic weights (≤ thousands) leave ample headroom
	}
}

// RoundTrip sends a request to the next weighted target.
func (l *WeightedRoundRobinLoadBalancer) RoundTrip(r *http.Request) (*http.Response, error) {
	l.once.Do(l.init)
	if len(l.peers) == 0 {
		return nil, ErrUnavailable
	}

	t := l.pick()
	r.URL.Host = t.Host
	return t.Transport.RoundTrip(r)
}

// pick runs one SWRR step under the lock and returns the chosen target. Each peer
// gains its weight, the largest currentWeight wins, and the winner gives back the
// total; the sum of all currentWeights is invariantly zero across a pick, so the
// ratios never drift. The lock covers only the integer loop — the network
// round-trip happens after it is released.
//
// With an active-HC gate, SWRR runs over the SURVIVORS only: gated-down peers are
// neither bumped nor selected, and the winner gives back the LIVE total (the sum of
// up peers' weights, not l.total) so the survivors' ratio stays exact — subtracting
// the full total here would drift it. A peer recovering (down->up) has its current
// reset to 0 so it cannot thunder-reinstate on its stale accumulator. If every peer
// is gated down it fails open to plain SWRR over all peers (a broken probe path
// must not black-hole a healthy pool).
func (l *WeightedRoundRobinLoadBalancer) pick() *Target {
	l.mu.Lock()
	defer l.mu.Unlock()

	var liveTotal int64
	best := -1
	for i := range l.peers {
		p := &l.peers[i]
		isUp := l.up(uint32(i))
		if isUp && !p.wasUp {
			p.current = 0 // just recovered: drop the stale accumulator
		}
		p.wasUp = isUp
		if !isUp {
			continue // gated down: do not bump or select; current frozen
		}
		p.current += p.weight
		liveTotal += p.weight
		if best < 0 || p.current > l.peers[best].current {
			best = i
		}
	}
	if best < 0 {
		return l.pickAllOpen() // every target down -> fail open over the whole pool
	}
	l.peers[best].current -= liveTotal // give back the LIVE total to preserve the ratio
	return l.peers[best].target
}

// pickAllOpen runs one plain SWRR step over every peer, ignoring the gate. Used
// only when the gate marked all targets down. The caller holds l.mu and has not
// bumped any peer this step (every peer was skipped), so this is a clean SWRR step.
func (l *WeightedRoundRobinLoadBalancer) pickAllOpen() *Target {
	best := -1
	for i := range l.peers {
		p := &l.peers[i]
		p.current += p.weight
		if best < 0 || p.current > l.peers[best].current {
			best = i
		}
	}
	l.peers[best].current -= l.total
	return l.peers[best].target
}

// setHealthGate installs the active health-check gate (see ActiveHealthCheck).
func (l *WeightedRoundRobinLoadBalancer) setHealthGate(gate []atomic.Bool) { l.gate = gate }

// up reports the active-HC verdict for target index i. A nil gate means "always
// up", so the hot path is unchanged for callers not using active health checks.
func (l *WeightedRoundRobinLoadBalancer) up(i uint32) bool {
	return l.gate == nil || int(i) >= len(l.gate) || l.gate[i].Load() // out-of-range => up (fail open, no panic)
}
