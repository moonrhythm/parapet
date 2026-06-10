package upstream

// ShedReason classifies why LeastConnLoadBalancer shed a request before any
// round-trip (it returned ErrUnavailable, which the proxy maps to 503). It is a
// CLOSED set — a bounded Prometheus label, never unbounded — reported via ShedFunc.
// A capacity shed is pool-wide (no single host), so it is named by reason only.
type ShedReason uint8

const (
	// ShedEmpty: the pool has no targets at all (len(peers) == 0). A
	// misconfiguration or torn-down pool, not load — distinct from a saturated one.
	ShedEmpty ShedReason = iota
	// ShedSaturated: every gate-up target is at its MaxConcurrent cap. The bulkhead
	// working as designed under overload — the brownout signal.
	ShedSaturated
	// ShedAllDark: the active-HC gate marked every target down and the fail-open
	// re-scan still found nothing admittable. A probe-dark/dead pool, distinct from a
	// merely-saturated healthy one. Unreachable without an active-HC gate installed
	// (a nil gate is "all up", so a no-HC pool sheds as ShedSaturated, never here).
	// The saturated/all_dark split is best-effort under health-state churn: a gate
	// that flips up between the gated scan and the fail-open re-scan can briefly
	// attribute a freshly-saturated shed to all_dark (the shed itself is unaffected).
	ShedAllDark
)

func (r ShedReason) String() string {
	switch r {
	case ShedSaturated:
		return "saturated"
	case ShedAllDark:
		return "all_dark"
	default:
		return "empty"
	}
}

// ShedFunc observes a LeastConnLoadBalancer shed: the balancer returned
// ErrUnavailable for the given reason before any round-trip. Assign one to
// LeastConnLoadBalancer.OnShed to make bulkhead saturation observable — see
// prom.UpstreamShed. It is invoked synchronously on the request goroutine at the
// shed site, exactly once per shed, before ErrUnavailable is returned, and never on
// a successful pick or on pick's internal CAS re-scan. Nil disables it at zero
// hot-path cost. Sheds can be high-frequency under sustained overload, so the callee
// must be cheap and allocation-free (the prom impl is a single counter Inc). The
// callee owns its own concurrency.
type ShedFunc func(ShedReason)

// TargetLoad is one target's live bulkhead occupancy, returned by
// LeastConnLoadBalancer.Inflight for scrape-time observability and tests. Cap is 0
// when the target is unbounded (Target.MaxConcurrent <= 0). Active is a point-in-time
// atomic load taken without locking, so across a returned slice the Active values are
// each individually exact but not a single frozen pool-wide instant — which is exactly
// what a per-target saturation gauge wants (each bulkhead is independent).
//
//nolint:govet // fields ordered for readability, not pointer-packing
type TargetLoad struct {
	Host   string // resolved upstream target (operator-configured, bounded label)
	Active int64  // in-flight requests right now
	Cap    int64  // MaxConcurrent bulkhead cap; 0 == unbounded
}

// Inflight returns a live snapshot of every target's current in-flight count and
// bulkhead cap, for scrape-time metrics (see prom.UpstreamInflight) or tests. It is
// safe to call concurrently with serving: each Active is a lone atomic load that adds
// no contention to the claim/dec hot path, and Cap is immutable after init. It is
// also safe before the first request — it forces init (l.once), so the configured
// targets are always present (Active 0 until traffic arrives), and a scrape that
// races the first RoundTrip never sees a nil peers slice. The returned slice is
// freshly allocated and owned by the caller; call it at scrape cadence, never on the
// request path.
func (l *LeastConnLoadBalancer) Inflight() []TargetLoad {
	l.once.Do(l.init) // config-only; idempotent with RoundTrip's own l.once.Do(l.init)
	out := make([]TargetLoad, len(l.peers))
	for i := range l.peers {
		p := &l.peers[i]
		out[i] = TargetLoad{Host: p.target.Host, Active: p.active.Load(), Cap: p.cap}
	}
	return out
}

// shed fires OnShed if wired. Cold and allocation-free; the nil-check keeps the shed
// path free of cost when no observer is attached.
func (l *LeastConnLoadBalancer) shed(reason ShedReason) {
	if l.OnShed != nil {
		l.OnShed(reason)
	}
}
