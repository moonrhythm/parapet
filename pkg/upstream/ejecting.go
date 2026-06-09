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
//nolint:govet // fields grouped by role (state, then config) for readability
type EjectingLoadBalancer struct {
	once    sync.Once
	i       atomic.Uint32
	targets []*ejectTarget
	gate    []atomic.Bool // active-HC gate; nil = all up (installed before serving)

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

	// OnStateChange observes a target being ejected (ReasonEject) or returned to
	// rotation on a confirmed success (ReasonRecover); nil disables it. It reflects
	// committed eject/recover events, NOT cooldown-expiry rotation membership: a
	// target whose cooldown has expired but has not yet served a successful request
	// still reads StateOpen until that success. Alert on the prom.UpstreamState
	// transitions counter, which is exact. The callee owns its own concurrency.
	OnStateChange StateChangeFunc
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
// ejected ones AND any the active-HC gate marks down. If all targets are
// out it falls open to the round-robin pick so traffic is never fully
// black-holed.
func (l *EjectingLoadBalancer) pick(n int) *ejectTarget {
	start := l.i.Add(1) - 1
	now := time.Now().UnixNano()
	for k := uint32(0); k < uint32(n); k++ {
		idx := (start + k) % uint32(n)
		t := l.targets[idx]
		if t.ejectedUntil.Load() <= now && l.up(idx) { // passive AND active
			return t
		}
	}
	return l.targets[start%uint32(n)]
}

// setHealthGate installs the active health-check gate (see ActiveHealthCheck).
func (l *EjectingLoadBalancer) setHealthGate(gate []atomic.Bool) { l.gate = gate }

// up reports the active-HC verdict for target index i. A nil gate means "always
// up", so the hot path is unchanged for callers not using active health checks.
func (l *EjectingLoadBalancer) up(i uint32) bool {
	return l.gate == nil || int(i) >= len(l.gate) || l.gate[i].Load() // out-of-range => up (fail open, no panic)
}

// record updates a target's health from a round-trip result.
func (l *EjectingLoadBalancer) record(t *ejectTarget, resp *http.Response, err error) {
	if l.failed(resp, err) {
		if int(t.fails.Add(1)) >= l.MaxFails {
			l.eject(t)
		}
		return
	}

	// Success: clear any failure/ejection state. Guard the writes behind reads so
	// the common all-healthy path only loads (shared cache lines) and never stores
	// (which would bounce the line exclusive across cores on every request).
	if t.fails.Load() != 0 || t.ejections.Load() != 0 || t.ejectedUntil.Load() != 0 {
		// Swap (not Store) so exactly one of several concurrent successes observes the
		// non-zero deadline and emits ReasonRecover — the recovery winner.
		wasEjected := t.ejectedUntil.Swap(0) != 0
		t.fails.Store(0)
		t.ejections.Store(0)
		if wasEjected && l.OnStateChange != nil {
			l.OnStateChange(StateChange{Host: t.target.Host, From: StateOpen, To: StateClosed, Reason: ReasonRecover})
		}
	}
}

func (l *EjectingLoadBalancer) failed(resp *http.Response, err error) bool {
	if l.IsFailure != nil {
		return l.IsFailure(resp, err)
	}
	return err != nil && !errors.Is(err, context.Canceled)
}

// eject takes a target out of rotation for an exponentially backed-off cooldown.
//
// Concurrent in-flight failures all cross the threshold at once, so this counts
// at most one ejection per down-window: it CAS-transitions ejectedUntil from a
// past value (selectable) to a future one, and whoever loses the race sees the
// window already open and returns without re-counting. So a burst of N
// simultaneous failures is one ejection, not N — the backoff exponent only grows
// when a cooldown expires and the target fails afresh.
func (l *EjectingLoadBalancer) eject(t *ejectTarget) {
	t.fails.Store(0) // threshold handled; reset the consecutive counter
	now := time.Now()
	for {
		prev := t.ejectedUntil.Load()
		if prev > now.UnixNano() {
			return // already ejected for this window
		}
		e := t.ejections.Load() + 1
		until := now.Add(l.ejectionTimeout(e)).UnixNano()
		if t.ejectedUntil.CompareAndSwap(prev, until) {
			t.ejections.Store(e)
			// A re-eject of a target that expired but never recovered (prev != 0) is a
			// self-loop from open; a fresh eject is from closed. Keeps the metric edges
			// chained given recover only fires on a confirmed success (see OnStateChange).
			if l.OnStateChange != nil {
				from := StateClosed
				if prev != 0 {
					from = StateOpen
				}
				l.OnStateChange(StateChange{Host: t.target.Host, From: from, To: StateOpen, Reason: ReasonEject})
			}
			return
		}
		// Lost the CAS to a concurrent ejector; re-read — its value is now in the
		// future, so the next iteration returns.
	}
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
