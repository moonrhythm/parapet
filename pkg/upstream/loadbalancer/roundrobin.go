package loadbalancer

import (
	"net/http"
	"sync/atomic"
)

// NewRoundRobin creates new round-robin load balancer
func NewRoundRobin(targets []*Target) *RoundRobin {
	return &RoundRobin{
		Targets: targets,
	}
}

// RoundRobin strategy
type RoundRobin struct {
	i uint32

	Targets []*Target
}

// RoundTrip sends a request to upstream server
func (l *RoundRobin) RoundTrip(r *http.Request) (*http.Response, error) {
	if len(l.Targets) == 0 {
		return badGateway{}.RoundTrip(r)
	}

	i := atomic.AddUint32(&l.i, 1) - 1
	i %= uint32(len(l.Targets))
	t := l.Targets[i]

	r.URL.Host = t.Host
	return t.Transport.RoundTrip(r)
}
