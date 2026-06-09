package upstream

import (
	"net/http"
	"sync"
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

	// Targets is the set of upstreams to balance across.
	Targets []*Target
}

// swrrPeer holds one target's smooth-weighted-round-robin state.
type swrrPeer struct {
	target  *Target
	weight  int64 // effective weight, >= 1; immutable after init
	current int64 // running currentWeight, guarded by the balancer's mu
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
func (l *WeightedRoundRobinLoadBalancer) pick() *Target {
	l.mu.Lock()
	best := -1
	for i := range l.peers {
		p := &l.peers[i]
		p.current += p.weight
		if best < 0 || p.current > l.peers[best].current {
			best = i
		}
	}
	l.peers[best].current -= l.total
	t := l.peers[best].target
	l.mu.Unlock()
	return t
}
