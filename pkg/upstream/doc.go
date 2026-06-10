// Package upstream is parapet's reverse-proxy and load-balancing layer: it turns a
// pool of backend targets into an http.RoundTripper that the proxy (upstream.New)
// drives, and layers a stack of reliability primitives on top of plain
// distribution.
//
// # The reliability stack
//
// Every balancer below is a drop-in http.RoundTripper for upstream.New, reads its
// configuration once before the first request, and tracks state with per-target
// atomics so the hot path stays lock-free. Two of them (ActiveHealthCheck and
// HedgingLoadBalancer) WRAP another balancer rather than owning targets directly,
// so the pieces compose (see "Composition" below).
//
//	Distribution (no health logic)
//	  RoundRobinLoadBalancer          even spread, every target equal
//	  WeightedRoundRobinLoadBalancer  bias request COUNT by Target.Weight (SWRR)
//	  LeastConnLoadBalancer           bias by live in-flight CONNECTIONS; honours
//	                                  Target.MaxConcurrent (the bulkhead cap)
//
//	Error-based reliability
//	  EjectingLoadBalancer            passive outlier ejection on consecutive
//	                                  failures, backed-off cooldown
//	  CircuitBreakingLoadBalancer     fail-FAST: reject an open target with no
//	                                  round-trip; Closed/Open/HalfOpen
//
//	Latency-based reliability
//	  LatencyEjectingLoadBalancer     eject a "gray failure" (200s but slow) on a
//	                                  decayed-mean TTFB vs the pool median
//	  HedgingLoadBalancer             speculative retry after HedgeDelay to cut
//	                                  tail latency (wraps any balancer)
//
//	Active probing (wraps any balancer)
//	  ActiveHealthCheck               out-of-band probes; gates the wrapped
//	                                  balancer's pick, only ever REMOVES candidates
//
// # Choosing a primitive by failure mode
//
// Pick by the failure you are defending against. An Upstream uses ONE balancer, so
// for the owning balancer choose the dominant failure mode (then compose the
// wrappers — active health and hedging — on top):
//
//	Failure mode                     Reach for
//	------------------------------   --------------------------------------------
//	flaky backend, hard 5xx/errors   EjectingLoadBalancer (keeps routing during a
//	                                 total outage) — or CircuitBreakingLoadBalancer
//	                                 if you would rather shed than hammer
//	dead / brownout origin, want     CircuitBreakingLoadBalancer (fail fast; an
//	to fail fast and shed            open target costs no connect+timeout)
//	tail latency (p99) on an         HedgingLoadBalancer (race a duplicate after
//	otherwise healthy pool           HedgeDelay, take the first answer)
//	gray failure: 200s but one       LatencyEjectingLoadBalancer (relative-to-pool
//	host is far slower than peers    median; error ejection / breakers miss it)
//	overload: a slow backend         Target.MaxConcurrent on LeastConnLoadBalancer
//	draining the pool                (hard per-target bulkhead cap) + a total-
//	                                 request-deadline middleware (see "Overload")
//	cold deploy / readiness /        ActiveHealthCheck (probe out-of-band; route
//	black-holing a fresh pod         only to answering targets) — wraps any balancer
//	uneven backend capacity          WeightedRoundRobinLoadBalancer (by request
//	                                 count) or LeastConnLoadBalancer (by concurrency)
//
// Error ejection (EjectingLoadBalancer / CircuitBreakingLoadBalancer) is driven by
// the IsFailure hook, which by default counts only transport errors other than a
// client cancel; set it to also treat 5xx as failures. LatencyEjectingLoadBalancer
// is latency-ONLY — a transport error is not timed and does not eject — so if hard
// errors are your dominant mode, use an error balancer instead of (or, via an
// ActiveHealthCheck gate, alongside) it.
//
// # All-down semantics — the load-bearing distinction
//
// When EVERY target is unavailable the primitives DIVERGE, and which one you ran
// decides whether a correlated outage degrades or hard-fails. This is the single
// most important property to know before an incident:
//
//	Primitive                       When all targets are out
//	-----------------------------   --------------------------------------------
//	RoundRobinLoadBalancer          FAIL OPEN — routes best-effort to the next
//	WeightedRoundRobin (SWRR)       slot (a broken signal must not black-hole a
//	LeastConnLoadBalancer (health)  healthy pool). Empty pool -> ErrUnavailable.
//	EjectingLoadBalancer            FAIL OPEN — all ejected -> route anyway, so a
//	LatencyEjectingLoadBalancer     transient outage / systemic slowdown can't
//	                                black-hole all traffic (slow-but-up beats 503).
//
//	LeastConnLoadBalancer           SHEDS 503 — when every target is at its
//	  (capacity, MaxConcurrent)     MaxConcurrent cap (the bulkhead contract;
//	                                independent of health — a probe-dark pool still
//	                                routes best-effort, a saturated one still sheds).
//	CircuitBreakingLoadBalancer     SHEDS 503 — every target open or probe-
//	                                saturated returns ErrUnavailable, deliberately
//	                                shedding load instead of hammering a dead origin.
//
// So the ejecting balancers and plain distribution route BEST-EFFORT under a total
// outage (preferring a degraded answer to none); the circuit breaker and the
// least-conn capacity cap SHED a 503. That is intentional: a latency outlier or a
// flaky host is still serving (route to it), but a hammered dead origin or a
// saturated pool is better protected by shedding. ActiveHealthCheck never adds an
// all-down OVERRIDE — when its gate marks every target down each balancer falls
// back to its OWN policy above, so a broken probe path cannot turn a fail-open pool
// into a black hole.
//
// A 503 surfaced this way is upstream.ErrUnavailable, which Upstream's error handler
// maps to HTTP 503 (any other transport error maps to 502), and which
// prom.Upstream() counts in upstream_fast_rejects_total before any round-trip.
//
// # Overload and the MaxConcurrent latch
//
// Target.MaxConcurrent (LeastConnLoadBalancer only) is a HARD per-target cap on
// in-flight requests — the bulkhead pattern. It bounds blast radius: a slow backend
// holds at most this many requests and cannot drain the pool; surplus routes to an
// under-cap target, and only when EVERY target is full does the balancer shed 503.
//
// A slot is held until the response BODY is closed, not at the response headers, so
// the cap bounds true end-to-end concurrency including slow body streams. Nothing
// else reclaims a slot: a backend that sends headers then stalls mid-body keeps its
// slot until the request context is cancelled. No http.Transport timeout covers
// that stall (ResponseHeaderTimeout bounds only time-to-headers; IdleConnTimeout
// reaps only idle pooled conns), and the existing write-header timeout in pkg/timeout
// disarms once upstream headers are written — so it does NOT cover a mid-body stall
// either. Without a bound on TOTAL request time, after MaxConcurrent stalled
// requests the cap becomes a LATCH, not a limiter, and the target sheds all traffic
// permanently. To defend against that, pair the cap with a total-request-deadline
// middleware in pkg/timeout (a request-scoped context deadline the transport honors,
// covering the whole request including the body) so a stalled request is cancelled
// and its slot reclaimed.
//
// # Composition
//
// The primitives COMPOSE; reach for more than one when you face more than one
// failure mode:
//
//   - ActiveHealthCheck wraps ANY balancer. It owns one probe goroutine per target
//     and publishes a per-target up/down gate that the wrapped balancer consults
//     inside its OWN pick. Active health only ever REMOVES candidates — it never
//     overrides the balancer's own (passive) verdict — so a target must satisfy BOTH
//     the active gate AND the balancer's strategy to be chosen: the two compose by
//     AND. The wrapped balancer keeps its exact strategy over the survivors (a
//     weighted balancer keeps its ratio, the breaker still trips, least-conn still
//     balances). Pass the SAME []*Target to both the balancer and the wrapper so the
//     gate's indices line up.
//
//   - HedgingLoadBalancer wraps ANY balancer. After HedgeDelay it duplicates the
//     in-flight (idempotent, body-less) request via a second call to the wrapped
//     balancer, which self-selects a different target, and returns the first winner.
//     Hedging and the proxy's retry loop layer rather than multiply. If the wrapped
//     balancer has a custom IsFailure, it MUST exclude context.Canceled or a
//     cancelled losing leg slowly ejects/trips the healthy backend it raced.
//
//   - Passive + active gate together by AND: e.g. EjectingLoadBalancer's pick takes
//     a target only when it is both not-ejected (passive) AND gate-up (active).
//
// A sensible production stack therefore reads outside-in as: ActiveHealthCheck ->
// (Hedging ->) a CircuitBreaking or Ejecting balancer over the target pool. See the
// runnable Example (ExampleNewActiveHealthCheck and the composition example) for how
// the pieces wire together.
//
// # Observability
//
// Two hooks make the stack observable; both are nil-by-default (zero hot-path cost)
// and the callee owns its own concurrency. Wire them to pkg/prom and leave
// pkg/upstream free of any Prometheus dependency:
//
//   - Upstream.OnRoundTrip (a RoundTripFunc) fires once per attempt (each retry
//     included) with the resolved host, status, time-to-headers, and error. Assign
//     prom.Upstream() to it for upstream_requests{host,status},
//     upstream_request_duration_seconds{host}, and upstream_fast_rejects_total{host}
//     (the all-down 503s shed before any round-trip).
//
//   - A balancer's OnStateChange (a StateChangeFunc) fires once per per-target
//     transition (concurrent threshold crossers collapse to one), after the new
//     state is published. Assign prom.UpstreamState() to it for
//     upstream_breaker_state{host} (gauge: 0 closed / 1 open / 2 half_open),
//     upstream_state_transitions_total{host,from,to,reason} (the authoritative
//     signal — alert on it), and upstream_probe_down_total{host,cause}. The cause
//     label is ActiveHealthCheck's classified probe-down reason — one of timeout,
//     refused, reset, dns, tls, status, error (a bounded closed set) — for telling a
//     bad probe path from a dead backend from a too-tight Timeout mid-incident.
//
// The ejecting balancers report ReasonEject / ReasonRecover; the circuit breaker
// reports the full Closed/Open/HalfOpen edge set (ReasonTrip, ReasonReopen,
// ReasonHeal, ReasonProbe, ReasonExpire); ActiveHealthCheck reports
// ReasonProbeDown (carrying the cause) and ReasonProbeRecover.
package upstream
