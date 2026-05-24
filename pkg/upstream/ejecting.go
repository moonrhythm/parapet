package upstream

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Ejecting load balancer defaults
const (
	defaultMaxFails        = 3
	defaultEjectTimeout    = 30 * time.Second
	defaultMaxEjectTimeout = 5 * time.Minute
)

// NewEjectingLoadBalancer creates a round-robin load balancer with passive
// health checking (outlier ejection).
func NewEjectingLoadBalancer(targets []*Target) *EjectingLoadBalancer {
	return &EjectingLoadBalancer{Targets: targets}
}

// EjectingLoadBalancer is a round-robin load balancer that passively tracks
// per-target failures and temporarily ejects targets that fail repeatedly.
//
// A target is ejected once it returns MaxFails consecutive failures, and stays
// ejected for EjectTimeout (doubling on each repeat ejection up to
// MaxEjectTimeout). Ejected targets are skipped during selection; when their
// cooldown expires they become selectable again with no background probing.
// A single successful response clears a target's failure count and backoff.
//
// If every target is ejected the balancer fails open and routes anyway, so a
// transient outage cannot black-hole all traffic.
//
// Configuration fields are read once, before the first request; set them
// before serving.
//
//nolint:govet
type EjectingLoadBalancer struct {
	once    sync.Once
	i       atomic.Uint32
	targets []*ejectTarget

	// Targets is the set of upstreams to balance across.
	Targets []*Target

	// MaxFails is the number of consecutive failures that ejects a target.
	// Defaults to 3.
	MaxFails int

	// EjectTimeout is the base cooldown a target stays ejected. It doubles on
	// each consecutive ejection, capped at MaxEjectTimeout. Defaults to 30s.
	EjectTimeout time.Duration

	// MaxEjectTimeout caps the ejection cooldown. Defaults to 5m.
	MaxEjectTimeout time.Duration

	// IsFailure decides whether a round-trip result counts as a failure. When
	// nil, any transport error other than a client-canceled request counts.
	// Set it to also treat responses such as 5xx as failures.
	IsFailure func(resp *http.Response, err error) bool
}

// ejectTarget holds the passive-health state for a single target.
type ejectTarget struct {
	target       *Target
	fails        atomic.Int32
	ejections    atomic.Int32
	ejectedUntil atomic.Int64 // unix nanos; <= now means selectable
}

func (l *EjectingLoadBalancer) init() {
	if l.MaxFails <= 0 {
		l.MaxFails = defaultMaxFails
	}
	if l.EjectTimeout <= 0 {
		l.EjectTimeout = defaultEjectTimeout
	}
	if l.MaxEjectTimeout <= 0 {
		l.MaxEjectTimeout = defaultMaxEjectTimeout
	}
	if l.MaxEjectTimeout < l.EjectTimeout {
		l.MaxEjectTimeout = l.EjectTimeout
	}

	l.targets = make([]*ejectTarget, len(l.Targets))
	for i, t := range l.Targets {
		l.targets[i] = &ejectTarget{target: t}
	}
}

// RoundTrip sends a request to a healthy upstream server.
func (l *EjectingLoadBalancer) RoundTrip(r *http.Request) (*http.Response, error) {
	l.once.Do(l.init)

	n := len(l.targets)
	if n == 0 {
		return nil, ErrUnavailable
	}

	t := l.pick(n)
	r.URL.Host = t.target.Host
	resp, err := t.target.Transport.RoundTrip(r)
	l.record(t, resp, err)
	return resp, err
}

// pick selects the next selectable target in round-robin order, skipping
// ejected ones. If all targets are ejected it falls open to the round-robin
// pick so traffic is never fully black-holed.
func (l *EjectingLoadBalancer) pick(n int) *ejectTarget {
	start := l.i.Add(1) - 1
	now := time.Now().UnixNano()
	for k := uint32(0); k < uint32(n); k++ {
		t := l.targets[(start+k)%uint32(n)]
		if t.ejectedUntil.Load() <= now {
			return t
		}
	}
	return l.targets[start%uint32(n)]
}

// record updates a target's health from a round-trip result.
func (l *EjectingLoadBalancer) record(t *ejectTarget, resp *http.Response, err error) {
	if l.failed(resp, err) {
		if int(t.fails.Add(1)) >= l.MaxFails {
			l.eject(t)
		}
		return
	}

	// success: clear failure count and backoff
	t.fails.Store(0)
	t.ejections.Store(0)
	t.ejectedUntil.Store(0)
}

func (l *EjectingLoadBalancer) failed(resp *http.Response, err error) bool {
	if l.IsFailure != nil {
		return l.IsFailure(resp, err)
	}
	return err != nil && !errors.Is(err, context.Canceled)
}

// eject takes a target out of rotation for an exponentially backed-off cooldown.
func (l *EjectingLoadBalancer) eject(t *ejectTarget) {
	t.fails.Store(0)
	e := t.ejections.Add(1)
	t.ejectedUntil.Store(time.Now().Add(l.ejectionTimeout(e)).UnixNano())
}

// ejectionTimeout returns EjectTimeout doubled for each prior ejection, capped
// at MaxEjectTimeout. e is the 1-based ejection count.
func (l *EjectingLoadBalancer) ejectionTimeout(e int32) time.Duration {
	d := l.EjectTimeout
	for i := int32(1); i < e && d < l.MaxEjectTimeout; i++ {
		d *= 2
	}
	if d <= 0 || d > l.MaxEjectTimeout {
		return l.MaxEjectTimeout
	}
	return d
}
