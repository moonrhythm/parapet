package upstream

import (
	"io"
	"net/http"
	"sync"
	"sync/atomic"
)

// NewLeastConnLoadBalancer creates a least-connection load balancer. Configuration
// fields are read once, before the first request; set them before serving.
func NewLeastConnLoadBalancer(targets []*Target) *LeastConnLoadBalancer {
	return &LeastConnLoadBalancer{Targets: targets}
}

// LeastConnLoadBalancer routes each request to the target holding the fewest
// in-flight requests, weighted by Weight: it minimizes active/Weight, so a target
// with twice the weight is kept at roughly twice the concurrency. Targets with
// equal load are served round-robin. It balances by concurrent CONNECTIONS (use
// WeightedRoundRobinLoadBalancer to balance by request count instead), which
// adapts to slow backends and long-lived requests that a round-robin count misses.
//
// A request stays counted as in-flight until its response body is closed, so it
// must be driven by something that closes the body — parapet's reverse proxy does.
// Used as a bare http.RoundTripper, the caller must close every response Body (the
// standard RoundTripper contract) or the target's active count leaks. With a
// Target.MaxConcurrent cap set, a leaked body is worse than skewed routing: it
// permanently burns a hard slot, so after MaxConcurrent leaks the target sheds all
// traffic (see the timeout warning on Target.MaxConcurrent).
//
//nolint:govet // fields grouped by role (state, then config) for readability
type LeastConnLoadBalancer struct {
	once  sync.Once
	i     atomic.Uint32 // rotation cursor for breaking equal-load ties
	peers []lcPeer
	gate  []atomic.Bool // active-HC gate; nil = all up (installed before serving)

	// Targets is the set of upstreams to balance across.
	Targets []*Target

	// OnShed observes a shed (this balancer returned ErrUnavailable before any
	// round-trip): ShedEmpty for no targets, ShedSaturated when every gate-up target
	// is at its MaxConcurrent cap, ShedAllDark when the active-HC gate marked the
	// whole pool down. Nil disables it at zero hot-path cost; see prom.UpstreamShed.
	// It fires synchronously on the request goroutine, before ErrUnavailable returns.
	OnShed ShedFunc
}

// lcPeer holds one target's least-connection state.
type lcPeer struct {
	target *Target
	weight int64        // effective weight, >= 1
	cap    int64        // Target.MaxConcurrent; 0 == unbounded (the bulkhead cap)
	active atomic.Int64 // in-flight requests
}

func (l *LeastConnLoadBalancer) init() {
	l.peers = make([]lcPeer, len(l.Targets))
	for i, t := range l.Targets {
		l.peers[i].target = t
		l.peers[i].weight = effectiveWeight(t)
		if t.MaxConcurrent > 0 { // <= 0 stays 0 (unbounded); read once, never on the hot path
			l.peers[i].cap = int64(t.MaxConcurrent)
		}
	}
}

// RoundTrip sends a request to the least-loaded target and keeps it counted as
// in-flight until the response body is closed.
func (l *LeastConnLoadBalancer) RoundTrip(r *http.Request) (*http.Response, error) {
	l.once.Do(l.init)
	n := len(l.peers)
	if n == 0 {
		l.shed(ShedEmpty)
		return nil, ErrUnavailable
	}

	p, ok, reason := l.pick(n)
	if !ok {
		l.shed(reason) // saturated (all at cap) or all_dark (probe-dark pool) -> shed (503)
		return nil, ErrUnavailable
	}
	// pick already claimed the slot (active +1) atomically, so the cap is never
	// exceeded; the matching decrement is the dec below (panic-safe + at body close).

	var once sync.Once
	dec := func() { once.Do(func() { p.active.Add(-1) }) }

	// Panic-safety: the increment above must be unwound on every exit. transferred
	// is set only once the decrement is handed off to the response body's Close, so
	// the defer releases the count on a transport panic (e.g. a nil Transport) or an
	// early return. dec is sync.Once-guarded, so the defer can never double-decrement
	// even when an inline branch also called it.
	transferred := false
	defer func() {
		if !transferred {
			dec()
		}
	}()

	r.URL.Host = p.target.Host
	resp, err := p.target.Transport.RoundTrip(r)

	if err != nil || resp == nil || resp.Body == nil {
		// No body to own (resp is nil on a real transport error); balance now. The
		// inline path and the wrapper path are mutually exclusive per attempt and
		// share one sync.Once, so exactly-once holds even for a misbehaving transport.
		dec()
		return resp, err
	}

	// Success: headers arrived but the upstream connection is held until the proxy
	// finishes streaming and closes the body, so hand the decrement to Body.Close.
	// Preserve the body's interface set: httputil.ReverseProxy type-asserts
	// res.Body.(io.ReadWriteCloser) on a 101 Switching Protocols upgrade, so a plain
	// io.ReadCloser wrapper would break WebSocket/upgrade traffic.
	if rwc, ok := resp.Body.(io.ReadWriteCloser); ok {
		resp.Body = &lcRWCBody{ReadWriteCloser: rwc, dec: dec}
	} else {
		resp.Body = &lcBody{ReadCloser: resp.Body, dec: dec}
	}
	transferred = true
	return resp, nil
}

// pick selects the least-loaded target that is UNDER its bulkhead cap and
// atomically claims a slot on it (active +1), so the slot is already held on
// return. It scans from a rotating cursor so equal-load targets are served
// round-robin, comparing active/weight via cross-multiplication (a/c.weight <
// bestA/best.weight  <=>  a*best.weight < bestA*c.weight) to stay integer-only.
// Targets at/over their cap are skipped; if every target is at its cap it returns
// ok=false and RoundTrip sheds (ErrUnavailable). The selection scan is read-only
// (atomic loads); only the claim mutates.
//
// pick is lock-free, not bounded by n scans: a re-scan happens only after a claim
// CAS observed the chosen peer already at cap, and concurrent releases make the
// candidate set non-monotonic. But total in-flight is bounded by sum(cap) and every
// losing CAS corresponds to a sibling that made progress (a claim or release), so
// the system is livelock-free.
func (l *LeastConnLoadBalancer) pick(n int) (*lcPeer, bool, ShedReason) {
	start := l.i.Add(1) - 1
	// failOpen ignores the active-HC gate for this pick. It engages only when the
	// gate has marked EVERY target down: a saturated-but-healthy pool sheds (the
	// bulkhead contract), but a fully probe-dark pool routes best-effort rather than
	// 503ing on a possibly-broken probe path. Nil gate => never engages, zero cost.
	failOpen := false
	for {
		var best *lcPeer
		var bestA int64
		sawUp := false // any gate-up peer seen THIS scan (whether or not under cap)
		for k := uint32(0); k < uint32(n); k++ {
			// uint64 so start+k can't wrap mid-scan when the cursor is near
			// MaxUint32: a uint32 add there would alias an index and skip a real
			// peer, false-shedding (503) if the skipped peer was the lone under-cap one.
			idx := uint32((uint64(start) + uint64(k)) % uint64(n))
			c := &l.peers[idx]
			if !failOpen {
				if l.up(idx) {
					sawUp = true
				} else {
					continue // active-HC down: skip (unless the whole pool is dark)
				}
			}
			a := c.active.Load()
			if c.cap != 0 && a >= c.cap {
				continue // at/over the bulkhead cap: skip, try the next target
			}
			if best == nil || a*best.weight < bestA*c.weight {
				best, bestA = c, a
			}
		}
		if best == nil {
			// Nothing selectable in THIS snapshot. sawUp distinguishes the two reasons
			// from the SAME scan (no second scan to race a concurrent gate flip): if no
			// peer was even up, the pool is dark -> fail open and re-scan ignoring the
			// gate; if some peer WAS up (just saturated), that's the bulkhead capacity
			// shed. A nil gate makes sawUp always true, so it sheds exactly as before.
			if !failOpen && !sawUp {
				failOpen = true
				continue
			}
			// sawUp here means this (possibly fail-open) scan saw a gate-up peer, all
			// at cap -> saturated; !sawUp means we reached here via the fail-open
			// re-scan with the whole pool gate-down -> all_dark. A nil gate makes
			// sawUp always true, so a no-HC pool always sheds saturated.
			if sawUp {
				return nil, false, ShedSaturated
			}
			return nil, false, ShedAllDark
		}
		if l.claim(best, bestA) {
			return best, true, 0 // reason ignored when ok==true
		}
		// best filled between the read and the CAS; re-scan (it will now be skipped).
	}
}

// setHealthGate installs the active health-check gate (see ActiveHealthCheck).
func (l *LeastConnLoadBalancer) setHealthGate(gate []atomic.Bool) { l.gate = gate }

// up reports the active-HC verdict for target index i. A nil gate means "always
// up", so the hot path is unchanged for callers not using active health checks. An
// out-of-range i (a gate sized to fewer targets than the balancer) is also treated
// as up, so a mis-wire fails open rather than panicking on the hot path.
func (l *LeastConnLoadBalancer) up(i uint32) bool {
	return l.gate == nil || int(i) >= len(l.gate) || l.gate[i].Load()
}

// claim atomically takes a slot on p if it is still under its cap, given the load
// pick already observed in expected. cap == 0 is unbounded (a plain increment, the
// uncapped fast path — behaviorally identical to today's leastconn). Otherwise the
// CAS commits only against the exact value observed, so the cap is HARD: under a
// burst active can never exceed cap. A CAS lost to a sibling release retries on the
// same peer (still the best choice); a peer that has since filled returns false so
// pick re-scans for another under-cap target.
func (l *LeastConnLoadBalancer) claim(p *lcPeer, expected int64) bool {
	if p.cap == 0 {
		p.active.Add(1)
		return true
	}
	for {
		if expected >= p.cap {
			return false // filled; caller re-scans
		}
		if p.active.CompareAndSwap(expected, expected+1) {
			return true
		}
		expected = p.active.Load() // lost the CAS to a sibling; re-read this peer
	}
}

// lcBody decrements the target's active count when the response body is closed.
type lcBody struct {
	io.ReadCloser
	dec func() // already sync.Once-guarded
}

func (b *lcBody) Close() error {
	err := b.ReadCloser.Close() // close underlying first (returns the conn to the pool)
	b.dec()
	return err
}

// lcRWCBody is lcBody for a body that is also writable, preserving the
// io.ReadWriteCloser interface so the 101 upgrade path in httputil.ReverseProxy
// still type-asserts the body successfully. Embedding only the interface erases
// the underlying conn's io.ReaderFrom, so io.Copy on the raw upgrade tunnel can't
// splice — a marginal throughput cost on WebSocket/upgrade traffic only, never on
// normal response bodies (which are copied via Read).
type lcRWCBody struct {
	io.ReadWriteCloser
	dec func() // already sync.Once-guarded
}

func (b *lcRWCBody) Close() error {
	err := b.ReadWriteCloser.Close()
	b.dec()
	return err
}
