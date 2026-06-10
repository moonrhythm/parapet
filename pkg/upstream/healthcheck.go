package upstream

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"syscall"
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

	// OnStateChange observes this target's active-health gate flipping: ReasonProbeDown
	// (UnhealthyThld consecutive failing probes took an up target down, From StateClosed
	// To StateOpen, carrying a classified ProbeCause) and ReasonProbeRecover (HealthyThld
	// consecutive successes readmitted a down one, From StateOpen To StateClosed,
	// CauseNone). nil disables it at zero cost. It fires synchronously on the target's
	// sole prober goroutine, exactly once per crossing, AFTER the gate bit is published;
	// the callee owns its own concurrency across targets (see prom.UpstreamState). The
	// initial gate (StartUnhealthy or not) is NOT a transition and fires nothing — with
	// StartUnhealthy the first admitting probe is a genuine ReasonProbeRecover. A
	// graceful shutdown / Close never emits a spurious ReasonProbeDown. The ProbeCause
	// label is a bounded closed set.
	OnStateChange StateChangeFunc
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
		// A malformed request config is a real failure, not a backend verdict — but
		// check the parent ctx first so a Close racing here is still suppressed.
		if ctx.Err() == nil {
			a.observe(pt, false, CauseError)
		}
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

	// Shutting down: the PARENT ctx (prober lifetime) was cancelled by Close /
	// graceful shutdown, so the in-flight RoundTrip returns context.Canceled. That is
	// not a backend verdict — drop it so Close never darkens the gate or fires a
	// spurious ReasonProbeDown on the way out. A per-probe Timeout differs: only pctx
	// expired, ctx.Err() is nil here, so it falls through as a real failure.
	if ctx.Err() != nil {
		return
	}

	ok := a.healthy(resp, rerr)
	var cause ProbeCause
	if !ok {
		cause = classifyProbeCause(resp, rerr)
	}
	a.observe(pt, ok, cause)
}

// observe applies one probe verdict to pt's consecutive-run counters, flips the
// published up bit on a threshold crossing, and (only then) fires OnStateChange for
// that crossing. cause classifies a failing probe and is consulted only on the down
// crossing (a recover carries CauseNone). Runs only on pt's own goroutine, so the run
// counters stay single-writer and the hook fires on the goroutine that commits the
// transition.
func (a *ActiveHealthCheck) observe(pt *probeTarget, ok bool, cause ProbeCause) {
	if ok {
		pt.failRun = 0
		pt.okRun++
		if pt.okRun >= a.HealthyThld && !pt.up.Load() {
			pt.up.Store(true) // down -> up (covers the StartUnhealthy first-success recover)
			if a.OnStateChange != nil {
				a.OnStateChange(StateChange{Host: pt.target.Host, From: StateOpen, To: StateClosed, Reason: ReasonProbeRecover})
			}
		}
		return
	}
	pt.okRun = 0
	pt.failRun++
	if pt.failRun >= a.UnhealthyThld && pt.up.Load() {
		pt.up.Store(false) // up -> down
		if a.OnStateChange != nil {
			a.OnStateChange(StateChange{Host: pt.target.Host, From: StateClosed, To: StateOpen, Reason: ReasonProbeDown, Cause: cause})
		}
	}
}

func (a *ActiveHealthCheck) healthy(resp *http.Response, err error) bool {
	if a.IsHealthy != nil {
		return a.IsHealthy(resp, err)
	}
	return err == nil && resp != nil && resp.StatusCode < 400
}

// classifyProbeCause maps a failed probe result to a bounded ProbeCause for the
// down-event label. Called only on the cold unhealthy path, after probe() has
// filtered the shutdown-cancel, so context.Canceled is unreachable here. The ladder
// is most-specific-first and traverses wrapping via errors.Is/errors.As; the
// catch-all is CauseError so the label set stays closed.
func classifyProbeCause(resp *http.Response, err error) ProbeCause {
	if err == nil {
		return CauseStatus // no transport error, but healthy() rejected the response (default: status >= 400, or a custom IsHealthy returned false)
	}
	// A *net.DNSError is a name-resolution failure by construction, so classify it as
	// dns regardless of whether it timed out. This MUST precede the DeadlineExceeded
	// rung: a resolver lookup timeout wraps context.DeadlineExceeded (Go 1.23+
	// DNSError.Unwrap), so the deadline rung would otherwise swallow it and mislabel a
	// DNS outage as a too-tight Timeout — the exact triage misdirection ProbeCause
	// exists to prevent. A genuinely too-tight Timeout still surfaces as timeout on the
	// non-DNS dial/response paths below.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return CauseDNS
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CauseTimeout // the per-probe pctx deadline (non-DNS); parent-cancel was already filtered in probe()
	}
	var certErr *tls.CertificateVerificationError
	var recErr tls.RecordHeaderError
	if errors.As(err, &certErr) || errors.As(err, &recErr) {
		return CauseTLS // cert distrust/expiry, or TLS spoken to a plaintext port (or vice-versa)
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return CauseRefused
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return CauseReset // peer reset or closed the connection unexpectedly
	}
	// Defensive catch-all for a non-stdlib net.Error that reports Timeout() without
	// wrapping context.DeadlineExceeded; stdlib transport timeouts already satisfy the
	// DeadlineExceeded rung above.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return CauseTimeout
	}
	return CauseError
}
