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
type RoundRobinLoadBalancer struct {
	Targets []*Target
	i       uint32
}

// RoundTrip sends a request to upstream server
func (l *RoundRobinLoadBalancer) RoundTrip(r *http.Request) (*http.Response, error) {
	if len(l.Targets) == 0 {
		return nil, ErrUnavailable
	}

	i := atomic.AddUint32(&l.i, 1) - 1
	i %= uint32(len(l.Targets))
	t := l.Targets[i]

	r.URL.Host = t.Host
	return t.Transport.RoundTrip(r)
}
