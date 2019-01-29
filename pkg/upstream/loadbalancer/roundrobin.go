package loadbalancer

import (
	"net/http"
	"sync"
)

// NewRoundRobin creates new round-robin load balancer
func NewRoundRobin(targets []*Target) http.RoundTripper {
	if len(targets) == 0 {
		return badGateway{}
	}
	return &RoundRobin{
		Targets: targets,
	}
}

// RoundRobin strategy
type RoundRobin struct {
	mu sync.Mutex
	i  int

	Targets []*Target
}

func (l *RoundRobin) RoundTrip(r *http.Request) (*http.Response, error) {
	l.mu.Lock()
	t := l.Targets[l.i]
	l.i++
	if l.i >= len(l.Targets) {
		l.i = 0
	}
	l.mu.Unlock()

	r.URL.Host = t.Host
	return t.Transport.RoundTrip(r)
}
