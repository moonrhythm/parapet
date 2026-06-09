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
// standard RoundTripper contract) or the target's active count leaks.
//
//nolint:govet // fields grouped by role (state, then config) for readability
type LeastConnLoadBalancer struct {
	once  sync.Once
	i     atomic.Uint32 // rotation cursor for breaking equal-load ties
	peers []lcPeer

	// Targets is the set of upstreams to balance across.
	Targets []*Target
}

// lcPeer holds one target's least-connection state.
type lcPeer struct {
	target *Target
	weight int64        // effective weight, >= 1
	active atomic.Int64 // in-flight requests
}

func (l *LeastConnLoadBalancer) init() {
	l.peers = make([]lcPeer, len(l.Targets))
	for i, t := range l.Targets {
		l.peers[i].target = t
		l.peers[i].weight = effectiveWeight(t)
	}
}

// RoundTrip sends a request to the least-loaded target and keeps it counted as
// in-flight until the response body is closed.
func (l *LeastConnLoadBalancer) RoundTrip(r *http.Request) (*http.Response, error) {
	l.once.Do(l.init)
	n := len(l.peers)
	if n == 0 {
		return nil, ErrUnavailable
	}

	p := l.pick(n)
	p.active.Add(1)

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

// pick scans for the target with the lowest active/weight, starting from a
// rotating cursor so equal-load targets are served round-robin. The comparison
// cross-multiplies (a/c.weight < bestA/best.weight  <=>  a*best.weight <
// bestA*c.weight) to stay integer-only; with realistic weights and concurrency the
// int64 products have ample headroom. It does atomic loads only — a momentary
// near-tie that misroutes self-corrects on the next pick.
func (l *LeastConnLoadBalancer) pick(n int) *lcPeer {
	start := l.i.Add(1) - 1
	best := &l.peers[start%uint32(n)]
	bestA := best.active.Load()
	for k := uint32(1); k < uint32(n); k++ {
		c := &l.peers[(start+k)%uint32(n)]
		a := c.active.Load()
		if a*best.weight < bestA*c.weight {
			best, bestA = c, a
		}
	}
	return best
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
