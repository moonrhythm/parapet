package upstream

import (
	"net/http"
	"sync/atomic"
)

// Target is the load balancer target
type Target struct {
	Transport http.RoundTripper
	Host      string
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
