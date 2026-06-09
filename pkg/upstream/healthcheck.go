package upstream

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moonrhythm/parapet"
)

// Active health-check defaults.
const (
	defaultHCProbePath          = "/"
	defaultHCProbeMethod        = http.MethodGet
	defaultHCProbeScheme        = "http"
	defaultHCProbeInterval      = 10 * time.Second
	defaultHCProbeTimeout       = 5 * time.Second
	defaultHCHealthyThreshold   = 1 // recover fast: one good probe re-admits
	defaultHCUnhealthyThreshold = 3 // matches EjectingLoadBalancer's defaultMaxFails

	// probePlaceholderHost is a syntactically valid authority used only to build the
	// probe request; the real target Host is assigned to req.URL.Host afterwards, so a
	// unix-socket path Host is never round-tripped through a URL string (which would
	// percent-encode its slashes and fail to parse).
	probePlaceholderHost = "healthcheck.invalid"
	// probeDrainLimit bounds how much of a probe response body is drained before Close
	// so the keep-alive connection is returned to the pool rather than dropped.
	probeDrainLimit = 4 << 10 // 4 KiB
)

// NewActiveHealthCheck wraps a balancer with background health probing. inner MUST
// be a balancer built over the SAME []*Target (same pointers, same order) as
// targets, so the health gate's indices line up — pass the identical slice to both,
// e.g. NewActiveHealthCheck(targets, NewEjectingLoadBalancer(targets)). Every
// balancer in this package implements the gate, so inner skips probe-down targets
// inside its OWN pick (preserving its strategy); a custom RoundTripper that does not
// implement it still gets probed, but probing cannot influence its routing.
func NewActiveHealthCheck(targets []*Target, inner http.RoundTripper) *ActiveHealthCheck {
	return &ActiveHealthCheck{Targets: targets, Balancer: inner}
}

// ActiveHealthCheck adds out-of-band health probing to a wrapped balancer. It is a
// drop-in http.RoundTripper for upstream.New: it owns one probe goroutine per
// target, and publishes each target's verdict into a shared []atomic.Bool "gate"
// that the wrapped balancer consults in its existing pick. Active health only ever
// REMOVES candidates — it never overrides the balancer's own (passive) verdict, so
// a target must satisfy BOTH the active gate AND the balancer's strategy to be
// chosen; the two compose by AND. When the gate marks a target down, the balancer
// routes around it per its own policy; when EVERY target is gated down the balancer
// fails open over its own all-down semantics (RoundRobin/Ejecting/LatencyEjecting/
// LeastConn route best-effort; CircuitBreaking still sheds 503) — active HC never
// adds an all-down override, so a broken probe path cannot black-hole a whole pool.
//
// Lifecycle: probing auto-starts on the first RoundTrip and (when served by a
// parapet.Server, via ServerContextKey) stops on graceful shutdown, like
// pkg/healthz. For a bare RoundTripper, or to fully close the shutdown-race window,
// call Start(ctx) before serving and Close() after. Close() drains every probe
// goroutine, so none outlives it.
//
// Configuration fields are read once, before the first probe; set them before
// serving.
//
//nolint:govet // fields grouped by role (state, then config) for readability
type ActiveHealthCheck struct {
	mu        sync.Mutex // guards the (closed, cancel, spawn) lifecycle decision
	startOnce sync.Once
	lazyOnce  sync.Once
	wg        sync.WaitGroup
	cancel    context.CancelFunc
	startCtx  context.Context // set by Start before start(); nil => lazy (Background)
	closed    bool
	explicit  bool          // Start(ctx) was called -> skip lazy shutdown registration
	up        []atomic.Bool // index-aligned to Targets; the shared gate
	probes    []*probeTarget

	// Targets is the set of upstreams to probe; MUST be the same slice the wrapped
	// Balancer was built over (see NewActiveHealthCheck).
	Targets []*Target

	// Balancer is the wrapped strategy. It receives the health gate if it implements
	// the internal gate interface (all package balancers do).
	Balancer http.RoundTripper

	// Path and Method are the probe request's URL path and HTTP method. Default "/"
	// and GET.
	Path   string
	Method string

	// Scheme is the probe URL scheme. It matters ONLY for targets backed by the
	// dynamic multi-scheme Transport, which dispatches on it (use "h2c" or "unix" to
	// match an h2c/unix data path); the dedicated transports (HTTPTransport,
	// HTTPSTransport, H2CTransport, UnixTransport) force their own scheme and ignore
	// it. Default "http". A wrong scheme here probes the wrong protocol and can drive
	// a healthy backend down, so set it (or ProbeTransport) for non-http data paths.
	Scheme string

	// Interval is the time between probes for a target. Default 10s.
	Interval time.Duration

	// Timeout bounds a single probe (a per-probe context deadline). Default 5s.
	Timeout time.Duration

	// HealthyThld is the number of consecutive successful probes that mark a down
	// target up again. Default 1 (recover fast).
	HealthyThld int

	// UnhealthyThld is the number of consecutive failed probes that mark an up target
	// down. Default 3.
	UnhealthyThld int

	// ProbeTransport overrides the transport used for probes; nil reuses each
	// Target.Transport (so the probe exercises the real connection pool/TLS). Set it
	// to isolate probe traffic from the data pool, or for exotic schemes. Note a probe
	// shares the data transport's per-host connection budget: with a very small
	// MaxConn (e.g. 1) an in-flight probe can hold the only connection for up to
	// Timeout, briefly blocking data requests to that origin — set ProbeTransport to
	// avoid the contention.
	ProbeTransport http.RoundTripper

	// StartUnhealthy makes targets begin DOWN (fail-closed cold start): only the first
	// successful probe admits them. Default false — targets begin up, so a
	// misconfigured probe path cannot black-hole a fresh deploy.
	StartUnhealthy bool

	// IsHealthy decides whether a probe result is healthy; nil treats a non-error
	// response with status < 400 as healthy.
	IsHealthy func(resp *http.Response, err error) bool
}

// probeTarget holds one target's probe state. up is the only cross-goroutine field
// (a pointer into the wrapper's gate slice); okRun/failRun are touched solely by
// that target's own probe goroutine, so they are plain ints (single-writer).
type probeTarget struct {
	target  *Target
	up      *atomic.Bool
	okRun   int
	failRun int
}

// activeHealthGate is implemented by the package balancers so ActiveHealthCheck can
// install a per-target up/down gate that the balancer's pick then consults. A nil
// gate (the default) means "all up", so the hot path is unchanged — one extra
// atomic load per candidate only when a gate is installed.
type activeHealthGate interface {
	setHealthGate(gate []atomic.Bool)
}

func (a *ActiveHealthCheck) init() {
	if a.Path == "" {
		a.Path = defaultHCProbePath
	}
	if a.Path[0] != '/' {
		a.Path = "/" + a.Path
	}
	if a.Method == "" {
		a.Method = defaultHCProbeMethod
	}
	if a.Scheme == "" {
		a.Scheme = defaultHCProbeScheme
	}
	if a.Interval <= 0 {
		a.Interval = defaultHCProbeInterval
	}
	if a.Timeout <= 0 {
		a.Timeout = defaultHCProbeTimeout
	}
	if a.HealthyThld <= 0 {
		a.HealthyThld = defaultHCHealthyThreshold
	}
	if a.UnhealthyThld <= 0 {
		a.UnhealthyThld = defaultHCUnhealthyThreshold
	}

	a.up = make([]atomic.Bool, len(a.Targets))
	a.probes = make([]*probeTarget, len(a.Targets))
	for i := range a.Targets {
		a.up[i].Store(!a.StartUnhealthy) // fail-open by default
		a.probes[i] = &probeTarget{target: a.Targets[i], up: &a.up[i]}
	}
	if g, ok := a.Balancer.(activeHealthGate); ok {
		g.setHealthGate(a.up) // inner balancer now skips probe-down targets in its pick
	}
}

// Start begins probing under ctx (cancel ctx, or call Close, to stop). Use it for a
// bare RoundTripper or to bound the prober's lifetime explicitly; otherwise the
// first RoundTrip auto-starts probing. Calling it after Close, or after a lazy
// start, is a no-op.
func (a *ActiveHealthCheck) Start(ctx context.Context) {
	a.mu.Lock()
	a.explicit = true
	if a.startCtx == nil {
		a.startCtx = ctx
	}
	a.mu.Unlock()
	a.start()
}

// start runs init + spawns the probe goroutines exactly once. The (closed, cancel,
// spawn) decision is made under mu so a Close racing a first-RoundTrip start can
// never leak: if Close set closed first, start spawns nothing; if start spawned
// first, wg.Add happened before Close's wg.Wait and cancel is published, so Close
// cancels and drains them.
func (a *ActiveHealthCheck) start() {
	a.startOnce.Do(func() {
		a.init()

		a.mu.Lock()
		if a.closed {
			// Close ran first: spawn nothing, and force the gate fail-open. init may have
			// stored an all-DOWN gate (StartUnhealthy) that no prober will ever lift —
			// for a shedding balancer (CircuitBreaking) that would 503 the pool forever.
			for i := range a.up {
				a.up[i].Store(true)
			}
			a.mu.Unlock()
			return
		}
		base := a.startCtx
		if base == nil {
			base = context.Background()
		}
		ctx, cancel := context.WithCancel(base)
		a.cancel = cancel
		a.wg.Add(len(a.probes)) // all Add before any possible wg.Wait in Close
		probes := a.probes
		a.mu.Unlock()

		for _, pt := range probes {
			go a.loop(ctx, pt)
		}
	})
}

// Close stops probing and drains every probe goroutine; idempotent. After Close,
// start is a no-op, so a late first-RoundTrip never resurrects the prober.
func (a *ActiveHealthCheck) Close() error {
	a.mu.Lock()
	a.closed = true
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
	a.wg.Wait()
	return nil
}

// RoundTrip starts probing (once), wires graceful shutdown on the lazy path, then
// defers to the wrapped balancer — the gate already filters its pick, so this never
// reroutes.
func (a *ActiveHealthCheck) RoundTrip(r *http.Request) (*http.Response, error) {
	a.lazyOnce.Do(func() {
		a.mu.Lock()
		explicit := a.explicit
		a.mu.Unlock()
		if explicit {
			return // caller owns the lifecycle via Start/Close
		}
		if srv, ok := r.Context().Value(parapet.ServerContextKey).(*parapet.Server); ok {
			srv.RegisterOnShutdown(func() { _ = a.Close() }) // same idiom as healthz
		}
	})
	a.start()

	if len(a.Targets) == 0 {
		return nil, ErrUnavailable
	}
	return a.Balancer.RoundTrip(r)
}

// loop probes one target immediately (fast cold-start convergence) then every
// Interval, until ctx is cancelled. Sequential, so a slow probe delays the next
// tick for its own target only and probes never stack.
func (a *ActiveHealthCheck) loop(ctx context.Context, pt *probeTarget) {
	defer a.wg.Done()
	if ctx.Err() != nil {
		return // Close raced start: never even probe once
	}
	a.probe(ctx, pt)

	t := time.NewTicker(a.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.probe(ctx, pt)
		}
	}
}

// probe issues one health request to pt's target and feeds the verdict to observe.
func (a *ActiveHealthCheck) probe(ctx context.Context, pt *probeTarget) {
	pctx, cancel := context.WithTimeout(ctx, a.Timeout)
	defer cancel()

	// Build with a placeholder authority, then assign the real Host and scheme directly
	// (as the balancers do, r.URL.Host = t.Host) so a unix-socket path Host is never
	// parsed from a URL string, and the dynamic Transport dispatches on the right scheme.
	req, err := http.NewRequestWithContext(pctx, a.Method, "http://"+probePlaceholderHost+a.Path, http.NoBody)
	if err != nil {
		a.observe(pt, false)
		return
	}
	req.URL.Scheme = a.Scheme
	req.URL.Host = pt.target.Host
	// Derive the Host header from URL.Host (the target's dial authority), not the
	// placeholder. Note this differs from the data path (which forwards the client's
	// Host): a backend that vhost-routes per request may need a custom IsHealthy or a
	// probe path that does not depend on the Host header.
	req.Host = ""

	tr := a.ProbeTransport
	if tr == nil {
		tr = pt.target.Transport
	}
	resp, rerr := tr.RoundTrip(req)
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, probeDrainLimit))
		_ = resp.Body.Close()
	}
	a.observe(pt, a.healthy(resp, rerr))
}

// observe applies one probe verdict to pt's consecutive-run counters and flips the
// published up bit on a threshold crossing. Runs only on pt's own goroutine.
func (a *ActiveHealthCheck) observe(pt *probeTarget, ok bool) {
	if ok {
		pt.failRun = 0
		pt.okRun++
		if pt.okRun >= a.HealthyThld && !pt.up.Load() {
			pt.up.Store(true)
		}
		return
	}
	pt.okRun = 0
	pt.failRun++
	if pt.failRun >= a.UnhealthyThld && pt.up.Load() {
		pt.up.Store(false)
	}
}

func (a *ActiveHealthCheck) healthy(resp *http.Response, err error) bool {
	if a.IsHealthy != nil {
		return a.IsHealthy(resp, err)
	}
	return err == nil && resp != nil && resp.StatusCode < 400
}
