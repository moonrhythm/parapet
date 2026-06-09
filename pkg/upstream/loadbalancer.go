package upstream

import (
	"net/http"
	"sync/atomic"
)

// Target is the load balancer target
type Target struct {
	Transport http.RoundTripper
	Host      string

	// Weight biases the weighted balancers toward this target:
	// WeightedRoundRobinLoadBalancer gives it a proportionally larger share of the
	// request COUNT; LeastConnLoadBalancer lets it hold a proportionally larger
	// share of concurrent in-flight requests. Values <= 0 are treated as 1.
	// RoundRobinLoadBalancer, EjectingLoadBalancer, and CircuitBreakingLoadBalancer
	// ignore this field and weight every target equally.
	Weight int

	// MaxConcurrent caps the in-flight requests LeastConnLoadBalancer routes to this
	// target (the bulkhead pattern), isolating blast radius: a slow backend can hold
	// at most this many in-flight requests and cannot drain the pool. A request
	// counts as in-flight until its response body is closed (not at the headers), so
	// the cap bounds true end-to-end concurrency including slow body streams. Beyond
	// the cap, requests route to another under-cap target; when EVERY target is at
	// its cap the balancer sheds (ErrUnavailable -> 503) rather than overloading a
	// saturated origin. The cap is hard — never exceeded, even under a concurrent
	// burst. Values <= 0 mean unbounded (the default). Only LeastConnLoadBalancer
	// honors it; the other balancers ignore it.
	//
	// WARNING: a slot is held until the response body is closed; nothing else
	// reclaims it. A backend that sends headers then stalls mid-body keeps its slot
	// until the request's context is cancelled (which closes the body). No
	// http.Transport timeout covers that stall: ResponseHeaderTimeout bounds only
	// time-to-headers, and IdleConnTimeout reaps only idle pooled connections, never
	// an in-flight stalled one. To bound a held slot you must cap TOTAL request time
	// so the context is cancelled mid-body — a request-scoped context deadline the
	// transport honors (note pkg/timeout disarms once upstream headers are written,
	// so it does NOT cover a mid-body stall). Without such a total-time bound, after
	// MaxConcurrent stalled requests the target sheds all traffic permanently — the
	// cap becomes a latch, not a limiter.
	MaxConcurrent int
}

// effectiveWeight normalizes a target's weight for the weighted balancers: a
// non-positive Weight means "unset" and counts as 1. It is the single source of
// the default rule, applied once at init so the hot path never re-reads Weight.
func effectiveWeight(t *Target) int64 {
	if t.Weight <= 0 {
		return 1
	}
	return int64(t.Weight)
}

// NewRoundRobinLoadBalancer creates new round-robin load balancer
func NewRoundRobinLoadBalancer(targets []*Target) *RoundRobinLoadBalancer {
	return &RoundRobinLoadBalancer{
		Targets: targets,
	}
}

// RoundRobinLoadBalancer strategy
//
//nolint:govet // fields grouped by role (state, then config) for readability
type RoundRobinLoadBalancer struct {
	i    uint32
	gate []atomic.Bool // active-HC gate; nil = all up (installed before serving)

	Targets []*Target
}

// RoundTrip sends a request to the next upstream server in round-robin order,
// skipping any the active-HC gate marks down; if every target is down it falls open
// to the next slot so traffic is never fully black-holed.
func (l *RoundRobinLoadBalancer) RoundTrip(r *http.Request) (*http.Response, error) {
	n := len(l.Targets)
	if n == 0 {
		return nil, ErrUnavailable
	}

	start := atomic.AddUint32(&l.i, 1) - 1
	t := l.Targets[start%uint32(n)] // fail-open default if every target is gated down
	for k := uint32(0); k < uint32(n); k++ {
		idx := (start + k) % uint32(n)
		if l.up(idx) {
			t = l.Targets[idx]
			break
		}
	}

	r.URL.Host = t.Host
	return t.Transport.RoundTrip(r)
}

// setHealthGate installs the active health-check gate (see ActiveHealthCheck).
func (l *RoundRobinLoadBalancer) setHealthGate(gate []atomic.Bool) { l.gate = gate }

// up reports the active-HC verdict for target index i. A nil gate means "always
// up", so the hot path is unchanged for callers not using active health checks. An
// out-of-range i — a gate sized to fewer targets than the balancer, i.e. a violated
// co-construction contract — is also treated as up, so a mis-wire fails open rather
// than panicking on the hot path.
func (l *RoundRobinLoadBalancer) up(i uint32) bool {
	return l.gate == nil || int(i) >= len(l.gate) || l.gate[i].Load()
}
