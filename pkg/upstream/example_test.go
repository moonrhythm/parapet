package upstream_test

import (
	"net/http"
	"time"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/upstream"
)

// Proxy every request to one backend. SingleHost is the common case: pick a
// transport for the wire protocol (HTTPTransport for plain HTTP/1.1 here) and
// point it at a host:port. The returned *Upstream is a parapet.Middleware.
func ExampleSingleHost() {
	s := parapet.New()
	s.Use(upstream.SingleHost("10.0.0.1:8080", &upstream.HTTPTransport{}))
}

// Tune the proxy and its transport: rewrite the upstream Host header, prefix a
// target path, cap the retry budget, and bound the dial / response timeouts.
func ExampleUpstream() {
	m := upstream.SingleHost("10.0.0.1:8080", &upstream.HTTPTransport{
		DialTimeout:           2 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          64,
	})
	m.Host = "api.internal" // Host header sent to the backend
	m.Path = "/v1"          // prefix joined ahead of the request path
	m.Retries = 2           // idempotent requests only; 0 disables retries
	m.BackoffFactor = 100 * time.Millisecond

	s := parapet.New()
	s.Use(m)
}

// Spread requests across a pool with plain round-robin. Each Target pairs a
// host:port with the transport used to reach it; upstream.New wraps the balancer
// (itself an http.RoundTripper) as the proxy's transport.
func ExampleNewRoundRobinLoadBalancer() {
	tr := &upstream.HTTPTransport{}
	lb := upstream.NewRoundRobinLoadBalancer([]*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: tr},
		{Host: "10.0.0.2:8080", Transport: tr},
		{Host: "10.0.0.3:8080", Transport: tr},
	})

	s := parapet.New()
	s.Use(upstream.New(lb))
}

// Bias traffic by capacity with weighted round-robin: a Weight of 3 receives
// three times the request share of a Weight of 1.
func ExampleNewWeightedRoundRobinLoadBalancer() {
	tr := &upstream.HTTPTransport{}
	lb := upstream.NewWeightedRoundRobinLoadBalancer([]*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: tr, Weight: 3}, // larger box
		{Host: "10.0.0.2:8080", Transport: tr, Weight: 1},
	})

	s := parapet.New()
	s.Use(upstream.New(lb))
}

// Route by live concurrency: each request goes to the target holding the fewest
// in-flight requests (scaled by Weight), which adapts to slow backends better
// than counting requests.
func ExampleNewLeastConnLoadBalancer() {
	// MaxConcurrent caps each target's in-flight requests (the bulkhead pattern):
	// the cap is hard, surplus routes to an under-cap target, and once every target
	// is full the balancer sheds with 503. A held slot is freed only when the body
	// is closed, so bound TOTAL request time (a request-context deadline the
	// transport honors) to keep a stalled backend from latching the cap — a
	// response-header timeout alone does not (see the Target.MaxConcurrent docs).
	tr := &upstream.HTTPTransport{ResponseHeaderTimeout: 5 * time.Second}
	lb := upstream.NewLeastConnLoadBalancer([]*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: tr, MaxConcurrent: 100},
		{Host: "10.0.0.2:8080", Transport: tr, MaxConcurrent: 100},
	})

	s := parapet.New()
	s.Use(upstream.New(lb))
}

// Add passive health checking: a target that returns repeated failures is
// ejected from rotation for a backed-off cooldown. Here IsFailure also counts
// 5xx responses, not just transport errors.
func ExampleNewEjectingLoadBalancer() {
	tr := &upstream.HTTPTransport{}
	lb := upstream.NewEjectingLoadBalancer([]*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: tr},
		{Host: "10.0.0.2:8080", Transport: tr},
	})
	lb.MaxFails = 5
	lb.EjectTimeout = 10 * time.Second
	lb.IsFailure = func(resp *http.Response, err error) bool {
		return err != nil || (resp != nil && resp.StatusCode >= 500)
	}

	s := parapet.New()
	s.Use(upstream.New(lb))
}

// Add circuit breaking: a target that fails repeatedly is opened and REJECTED
// without a round-trip (fail fast), then probed for recovery via a half-open
// trickle. Unlike ejection, when every target is open it returns 503 rather than
// hammering a dead origin. Here IsFailure also counts 5xx as failures.
func ExampleNewCircuitBreakingLoadBalancer() {
	tr := &upstream.HTTPTransport{}
	lb := upstream.NewCircuitBreakingLoadBalancer([]*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: tr},
		{Host: "10.0.0.2:8080", Transport: tr},
	})
	lb.FailureThreshold = 5
	lb.OpenTimeout = 10 * time.Second
	lb.IsFailure = func(resp *http.Response, err error) bool {
		return err != nil || (resp != nil && resp.StatusCode >= 500)
	}

	s := parapet.New()
	s.Use(upstream.New(lb))
}

// Catch a "gray failure" — a backend still returning 200s but far slower than its
// peers — that error-based ejection misses. A target whose mean latency exceeds
// EjectionFactor x the pool median is ejected and re-probed. A uniform slowdown
// ejects no one (it self-tunes against the pool median).
func ExampleNewLatencyEjectingLoadBalancer() {
	tr := &upstream.HTTPTransport{}
	lb := upstream.NewLatencyEjectingLoadBalancer([]*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: tr},
		{Host: "10.0.0.2:8080", Transport: tr},
		{Host: "10.0.0.3:8080", Transport: tr},
	})
	lb.EjectionFactor = 3 // eject a target 3x slower than the pool median
	lb.HalfLife = 10 * time.Second

	s := parapet.New()
	s.Use(upstream.New(lb))
}

// Cut tail latency by hedging: wrap any balancer so a slow idempotent request is
// duplicated to another target after HedgeDelay, taking the first response. It
// composes with every balancer (here round-robin). If the wrapped balancer uses a
// custom IsFailure, exclude context.Canceled so cancelled losing legs don't eject
// the healthy backend they raced.
func ExampleNewHedgingLoadBalancer() {
	tr := &upstream.HTTPTransport{}
	lb := upstream.NewRoundRobinLoadBalancer([]*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: tr},
		{Host: "10.0.0.2:8080", Transport: tr},
	})
	h := upstream.NewHedgingLoadBalancer(lb)
	h.HedgeDelay = 30 * time.Millisecond // ~p95; <= 0 disables

	s := parapet.New()
	s.Use(upstream.New(h)) // h is the proxy's transport, like any balancer
}

// Add ACTIVE health checking: probe each target out-of-band and route only to those
// answering, on top of any balancer's own (passive) strategy. Pass the SAME []*Target
// to both the balancer and the wrapper so the health gate's indices line up. Active
// HC only removes candidates; the balancer keeps its strategy over the survivors and
// composes with passive ejection. When served by a parapet.Server it stops on
// graceful shutdown automatically; call Start(ctx)/Close() for explicit control.
func ExampleNewActiveHealthCheck() {
	tr := &upstream.HTTPTransport{}
	targets := []*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: tr},
		{Host: "10.0.0.2:8080", Transport: tr},
	}
	ahc := upstream.NewActiveHealthCheck(targets, upstream.NewRoundRobinLoadBalancer(targets))
	ahc.Path = "/healthz"
	ahc.Interval = 5 * time.Second
	ahc.UnhealthyThld = 3 // down after 3 consecutive failed probes
	// Observe gate flips (probe-down with a classified cause / probe-recover); wire to
	// prom.UpstreamState in production for upstream_probe_down_total{host,cause}.
	ahc.OnStateChange = func(c upstream.StateChange) {
		_ = c.Reason // ReasonProbeDown or ReasonProbeRecover
		_ = c.Cause  // classified failure cause on a down event, e.g. "timeout"
	}

	s := parapet.New()
	s.Use(upstream.New(ahc)) // ahc is the proxy's transport, like any balancer
}

// Observe each origin round-trip via OnRoundTrip — invoked once per attempt with
// the resolved target, status, latency, and error. Wire it to metrics or logging
// (see prom.Upstream); here it just inspects the info.
func ExampleUpstream_onRoundTrip() {
	m := upstream.SingleHost("10.0.0.1:8080", &upstream.HTTPTransport{})
	m.OnRoundTrip = func(r *http.Request, info upstream.RoundTripInfo) {
		_ = info.Host     // resolved upstream target
		_ = info.Status   // response status, or 0 on a pre-response failure
		_ = info.Duration // time to response headers
		_ = info.Err      // transport error, or nil once a response arrived
	}

	s := parapet.New()
	s.Use(m)
}

// Compose a production reliability stack: ACTIVE health probing wrapping a CIRCUIT
// BREAKER over the pool. The breaker fails fast (an open target costs no round-trip)
// and sheds 503 when every target is open; active health probes out-of-band and only
// REMOVES probe-down targets from the breaker's pick — the two gate together by AND.
// Pass the SAME []*Target to both so their indices line up, and wire each layer's
// OnStateChange to prom.UpstreamState() so trips, recoveries, and probe-downs are
// observable. See pkg/upstream's package doc for the failure-mode / all-down-
// semantics guide this example illustrates.
func ExampleNewActiveHealthCheck_compose() {
	tr := &upstream.HTTPTransport{}
	targets := []*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: tr},
		{Host: "10.0.0.2:8080", Transport: tr},
		{Host: "10.0.0.3:8080", Transport: tr},
	}

	// Inner: a circuit breaker that fails fast and sheds 503 on a correlated outage.
	cb := upstream.NewCircuitBreakingLoadBalancer(targets)
	cb.FailureThreshold = 5
	cb.OpenTimeout = 5 * time.Second
	cb.IsFailure = func(resp *http.Response, err error) bool {
		return err != nil || (resp != nil && resp.StatusCode >= 500)
	}
	cb.OnStateChange = func(c upstream.StateChange) {
		_ = c.From // wire to prom.UpstreamState() in production
		_ = c.To
		_ = c.Reason // ReasonTrip / ReasonHeal / ReasonProbe / ...
	}

	// Outer: active probing that gates the breaker's pick (only removes candidates).
	ahc := upstream.NewActiveHealthCheck(targets, cb)
	ahc.Path = "/healthz"
	ahc.Interval = 5 * time.Second
	ahc.UnhealthyThld = 3
	ahc.OnStateChange = func(c upstream.StateChange) {
		_ = c.Reason // ReasonProbeDown / ReasonProbeRecover
		_ = c.Cause  // classified failure cause on a probe-down (e.g. "timeout")
	}

	u := upstream.New(ahc) // ahc is the proxy's transport, like any balancer
	u.OnRoundTrip = func(r *http.Request, info upstream.RoundTripInfo) {
		_ = info.Host   // resolved target; "" when shed before any pick
		_ = info.Status // wire to prom.Upstream() in production
	}

	s := parapet.New()
	s.Use(u)
}

// Pick the transport for the backend's protocol. Transport auto-selects per the
// request URL scheme (http/https, h2c for cleartext HTTP/2, unix sockets), so one
// instance can front a mixed pool; the dedicated transports pin a single protocol.
func ExampleTransport() {
	s := parapet.New()
	s.Use(upstream.SingleHost("10.0.0.1:8080", &upstream.Transport{
		MaxIdleConns:       64,
		DisableCompression: true,
	}))
}
