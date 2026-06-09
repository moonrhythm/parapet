package upstream

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Circuit-breaker states, held in the low 2 bits of cbState.word.
const (
	cbClosed   uint64 = iota // route normally; count failures
	cbOpen                   // reject without a round-trip until the cooldown expires
	cbHalfOpen               // admit a limited number of trial probes
)

// Packed-word layout: gen<<cbGenShift | probes<<cbStateBits | state. Keeping the
// in-flight probe count IN the same atomic word as the state and generation makes
// a probe-slot claim atomic with the (gen, state) it belongs to — the only design
// that bounds the half-open probe cap under contention (a separate atomic counter
// leaks across the open->half-open edge and the re-open boundary).
const (
	cbStateBits = 2
	cbProbeBits = 22
	cbStateMask = (1 << cbStateBits) - 1
	cbProbeMask = ((1 << cbProbeBits) - 1) << cbStateBits
	cbProbeOne  = uint64(1) << cbStateBits // increment unit for the probes field
	cbGenShift  = cbStateBits + cbProbeBits // 24
	cbMaxProbes = (1 << cbProbeBits) - 1    // ~4M; init clamps HalfOpenMaxProbes to this
)

// Circuit breaker defaults.
const (
	defaultFailureThreshold  = 5
	defaultSuccessThreshold  = 2
	defaultCBOpenTimeout     = 5 * time.Second
	defaultCBMaxOpenTimeout  = time.Minute
	defaultHalfOpenMaxProbes = 1
	defaultProbeTimeout      = 2 * time.Minute // above the transports' 1m default response timeout
)

func cbPack(gen, probes uint32, state uint64) uint64 {
	return uint64(gen)<<cbGenShift | uint64(probes)<<cbStateBits | state
}

func cbUnpack(v uint64) (gen, probes uint32, state uint64) {
	return uint32(v >> cbGenShift), uint32((v & cbProbeMask) >> cbStateBits), v & cbStateMask
}

// NewCircuitBreakingLoadBalancer creates a round-robin load balancer with a
// per-target circuit breaker.
func NewCircuitBreakingLoadBalancer(targets []*Target) *CircuitBreakingLoadBalancer {
	return &CircuitBreakingLoadBalancer{Targets: targets}
}

// CircuitBreakingLoadBalancer is a round-robin load balancer that wraps each
// target in a circuit breaker. Unlike EjectingLoadBalancer (which keeps routing —
// paying the full connect+timeout — to a degraded target and fails open when all
// targets are out), this REJECTS a request to an open target without a round-trip:
// pick skips it and selects a healthy peer in the same call, for every method.
//
// Each target moves through the canonical three states:
//   - CLOSED: routes normally; FailureThreshold consecutive failures trip it to OPEN.
//   - OPEN: skipped during selection (fail fast, no round-trip) for OpenTimeout,
//     which backs off (doubling, capped at MaxOpenTimeout) on each repeat trip.
//   - HALF-OPEN: admits up to HalfOpenMaxProbes trial requests; SuccessThreshold
//     consecutive successes close it, one failure re-opens it with a longer backoff.
//     A single success clears a CLOSED target's failure count.
//
// When EVERY target is open (or probe-saturated) it returns ErrUnavailable (a 503)
// rather than failing open — deliberately shedding load instead of hammering a dead
// origin during a correlated outage. Use EjectingLoadBalancer if you want fail-open.
//
// It is a drop-in http.RoundTripper for upstream.New, ignores Target.Weight (it is
// a plain round-robin balancer), and tracks state with per-target atomics so the
// hot path is lock-free. A failure is observed at the response headers (a transport
// error, or via IsFailure), not at body close — so a backend that returns 200
// headers then fails mid-body is not seen as a failure (set IsFailure to catch 5xx
// at the status line). Configuration fields are read once; set them before serving.
//
//nolint:govet // fields grouped by role (state, then config) for readability
type CircuitBreakingLoadBalancer struct {
	once     sync.Once
	i        atomic.Uint32 // round-robin cursor
	breakers []cbState     // per-target FSM, built in init()

	// Targets is the set of upstreams to balance across.
	Targets []*Target

	// FailureThreshold is the number of consecutive failures that trips a CLOSED
	// target to OPEN. Defaults to 5.
	FailureThreshold int

	// SuccessThreshold is the number of consecutive HALF-OPEN probe successes that
	// closes a target. Defaults to 2.
	SuccessThreshold int

	// OpenTimeout is the base cooldown a target stays OPEN before a probe is allowed.
	// It doubles on each repeat trip, capped at MaxOpenTimeout. Defaults to 5s.
	OpenTimeout time.Duration

	// MaxOpenTimeout caps the backed-off OPEN cooldown. Defaults to 1m.
	MaxOpenTimeout time.Duration

	// ProbeTimeout bounds how long a HALF-OPEN probe may hold its slot. If a probe
	// does not complete within it (e.g. a transport with no response timeout hangs),
	// its slot is reclaimed and the target re-opens, so one stuck probe cannot wedge
	// the target half-open forever. Set it above the upstream transport's response
	// timeout so it only fires for genuinely hung probes. Defaults to 2m (above the
	// HTTP transports' 1m default response-header timeout).
	ProbeTimeout time.Duration

	// HalfOpenMaxProbes is the maximum number of concurrent HALF-OPEN probes (a
	// connect+headers admission cap, not an in-flight-body cap). Defaults to 1.
	HalfOpenMaxProbes int32

	// IsFailure decides whether a round-trip result counts as a failure. When nil,
	// any transport error other than a client-canceled request counts. Set it to
	// also treat responses such as 5xx as failures. In HALF-OPEN a non-failure
	// counts as a probe success, so by default a client-canceled probe nudges the
	// breaker toward closing rather than being treated as neutral (a wrongly closed
	// but still-broken target simply re-trips on the next real failure).
	IsFailure func(resp *http.Response, err error) bool

	// OnStateChange observes per-target circuit state transitions (nil disables);
	// see prom.UpstreamState. It is fired synchronously from the goroutine that
	// commits the transition, exactly once per transition, after the new state is
	// published. The callee owns its own concurrency.
	OnStateChange StateChangeFunc
}

// cbState is one target's circuit-breaker state. word is the single source of
// truth for (generation, in-flight probes, state); the rest are satellites whose
// cross-word ordering is handled explicitly (see transition).
//
//nolint:govet // fields grouped by role for readability
type cbState struct {
	target        *Target
	word          atomic.Uint64 // gen<<24 | probes<<2 | state
	openedUntil   atomic.Int64  // OPEN cooldown deadline (unix nanos)
	halfOpenSince atomic.Int64  // HALF-OPEN entry time (unix nanos), for hung-probe reclaim
	generations   atomic.Int32  // consecutive OPEN episodes -> backoff exponent
	failures      atomic.Int32  // CLOSED consecutive failures
	successes     atomic.Int32  // HALF-OPEN consecutive probe successes
}

// cbAdmission is how a request was admitted, threaded from pick to record.
type cbAdmission uint8

const (
	cbAdmitClosed cbAdmission = iota // routed in CLOSED
	cbAdmitProbe                     // admitted as a HALF-OPEN probe (holds a slot)
)

func (l *CircuitBreakingLoadBalancer) init() {
	if l.FailureThreshold <= 0 {
		l.FailureThreshold = defaultFailureThreshold
	}
	if l.SuccessThreshold <= 0 {
		l.SuccessThreshold = defaultSuccessThreshold
	}
	if l.OpenTimeout <= 0 {
		l.OpenTimeout = defaultCBOpenTimeout
	}
	if l.MaxOpenTimeout <= 0 {
		l.MaxOpenTimeout = defaultCBMaxOpenTimeout
	}
	if l.MaxOpenTimeout < l.OpenTimeout {
		l.MaxOpenTimeout = l.OpenTimeout
	}
	if l.ProbeTimeout <= 0 {
		l.ProbeTimeout = defaultProbeTimeout
	}
	if l.HalfOpenMaxProbes <= 0 {
		l.HalfOpenMaxProbes = defaultHalfOpenMaxProbes
	}
	if l.HalfOpenMaxProbes > cbMaxProbes {
		l.HalfOpenMaxProbes = cbMaxProbes
	}

	l.breakers = make([]cbState, len(l.Targets))
	for i, t := range l.Targets {
		l.breakers[i].target = t // zero word = (gen 0, probes 0, CLOSED)
	}
}

// RoundTrip routes the request to a healthy target, skipping open ones, and
// records the outcome against the chosen target's breaker.
func (l *CircuitBreakingLoadBalancer) RoundTrip(r *http.Request) (*http.Response, error) {
	l.once.Do(l.init)

	n := len(l.breakers)
	if n == 0 {
		return nil, ErrUnavailable
	}

	b, gen, adm, ok := l.pick(n)
	if !ok {
		return nil, ErrUnavailable // every target open-cooling or probe-saturated -> 503
	}

	// Panic-safety: a probe holds a slot in the word until record() runs. If
	// RoundTrip panics, record() never runs, so release the slot on unwind — else a
	// cap=1 breaker would wedge half-open. recorded guards against a double release
	// (the normal path's record already advanced or freed the slot). A hung (never
	// returning) RoundTrip is handled separately by the ProbeTimeout reclaim.
	recorded := false
	if adm == cbAdmitProbe {
		defer func() {
			if !recorded {
				l.releaseProbe(b, gen)
			}
		}()
	}

	r.URL.Host = b.target.Host
	resp, err := b.target.Transport.RoundTrip(r)
	l.record(b, gen, adm, resp, err)
	recorded = true
	return resp, err
}

// pick scans round-robin for the first admissible target. Open-cooling and
// probe-saturated targets are skipped (no round-trip). If none are admissible it
// returns ok=false, which RoundTrip turns into ErrUnavailable (a 503) — shedding
// load rather than failing open.
func (l *CircuitBreakingLoadBalancer) pick(n int) (*cbState, uint32, cbAdmission, bool) {
	start := l.i.Add(1) - 1
	now := time.Now().UnixNano()
	for k := uint32(0); k < uint32(n); k++ {
		b := &l.breakers[(start+k)%uint32(n)]
		if gen, adm, ok := l.admit(b, now); ok {
			return b, gen, adm, true
		}
	}
	return nil, 0, 0, false
}

// admit decides whether the breaker will accept a request now. CLOSED always
// admits; OPEN admits nothing while cooling and otherwise flips one picker to
// HALF-OPEN; HALF-OPEN admits up to HalfOpenMaxProbes probes (and reclaims a stuck
// probe's slot past ProbeTimeout). The returned gen stamps a probe so its result
// can be matched to the generation it was admitted under.
func (l *CircuitBreakingLoadBalancer) admit(b *cbState, now int64) (uint32, cbAdmission, bool) {
	for {
		v := b.word.Load()
		gen, probes, state := cbUnpack(v)
		switch state {
		case cbClosed:
			return 0, cbAdmitClosed, true

		case cbOpen:
			if b.openedUntil.Load() > now {
				return 0, 0, false // cooling: fail fast, skip
			}
			// Cooldown expired: flip to HALF-OPEN. Stamp the dwell start BEFORE the
			// CAS so any picker that observes HALF-OPEN reads a fresh halfOpenSince
			// (sequentially-consistent atomics). Exactly one picker wins the CAS; it
			// does NOT pre-claim a slot but falls through and competes for one below,
			// so winner and losers admit uniformly (no thundering herd, and no
			// successes/probe pre-claim race).
			b.halfOpenSince.Store(now)
			if b.word.CompareAndSwap(v, cbPack(gen+1, 0, cbHalfOpen)) {
				l.emit(b, StateOpen, StateHalfOpen, ReasonProbe) // single CAS winner
			}
			continue

		default: // cbHalfOpen
			if probes >= uint32(l.HalfOpenMaxProbes) {
				// Budget full. Reclaim the slot of a probe that has overstayed
				// ProbeTimeout (a hung, never-returning round-trip), re-arming the
				// cooldown; the stale probe's eventual record is generation-inert.
				if now-b.halfOpenSince.Load() > int64(l.ProbeTimeout) {
					l.transition(b, gen, cbHalfOpen, cbOpen, ReasonExpire)
					continue
				}
				return 0, 0, false // fail fast, skip
			}
			// Claim a slot by incrementing the probes field IN the word, atomic with
			// the (gen, state) it belongs to. The cap is checked against the exact
			// word the CAS commits, so probes never exceeds the cap within a gen.
			if b.word.CompareAndSwap(v, v+cbProbeOne) {
				return gen, cbAdmitProbe, true
			}
			continue // word moved (a sibling claimed/freed a slot); re-read
		}
	}
}

// record updates a breaker from a round-trip result. A probe success/failure
// drives the HALF-OPEN -> CLOSED/OPEN transitions; a CLOSED failure counts toward
// the trip; a CLOSED success clears the failure count.
func (l *CircuitBreakingLoadBalancer) record(b *cbState, gen uint32, adm cbAdmission, resp *http.Response, err error) {
	failed := l.failed(resp, err)

	if adm == cbAdmitProbe {
		switch {
		case failed:
			l.transition(b, gen, cbHalfOpen, cbOpen, ReasonReopen) // re-open; word-CAS reclaims all slots
		case int(b.successes.Add(1)) >= l.SuccessThreshold:
			l.transition(b, gen, cbHalfOpen, cbClosed, ReasonHeal) // heal; word-CAS reclaims all slots
		default:
			l.releaseProbe(b, gen) // sub-threshold success: free just this slot
		}
		return
	}

	// cbAdmitClosed
	if failed {
		if int(b.failures.Add(1)) >= l.FailureThreshold {
			// Re-read and trip only from CLOSED, so a concurrent burst of
			// threshold-crossers collapses to exactly one trip per down-window.
			g, _, state := cbUnpack(b.word.Load())
			if state == cbClosed {
				l.transition(b, g, cbClosed, cbOpen, ReasonTrip)
			}
		}
		return
	}
	// Success: read-guarded reset so the healthy path only loads (no store to bounce
	// the cache line across cores).
	if b.failures.Load() != 0 {
		b.failures.Store(0)
	}
}

func (l *CircuitBreakingLoadBalancer) failed(resp *http.Response, err error) bool {
	if l.IsFailure != nil {
		return l.IsFailure(resp, err)
	}
	return err != nil && !errors.Is(err, context.Canceled)
}

// cbExternal maps the internal packed state to the public State.
func cbExternal(s uint64) State {
	switch s {
	case cbOpen:
		return StateOpen
	case cbHalfOpen:
		return StateHalfOpen
	default:
		return StateClosed
	}
}

// emit reports a state transition to OnStateChange, if set.
func (l *CircuitBreakingLoadBalancer) emit(b *cbState, from, to State, reason Reason) {
	if l.OnStateChange != nil {
		l.OnStateChange(StateChange{Host: b.target.Host, From: from, To: to, Reason: reason})
	}
}

// transition advances a breaker from (gen, from) to (gen+1, next), zeroing the
// probe count and the satellite counters. It is a no-op if the breaker has already
// left (gen, from) — so concurrent callers collapse to exactly one transition per
// generation. For ->OPEN it publishes openedUntil BEFORE the word CAS, so a picker
// that observes OPEN is guaranteed (SC atomics) to read the fresh cooldown deadline
// rather than a stale-expired one; generations is committed only after the CAS wins
// so a lost race never over-counts the backoff exponent.
func (l *CircuitBreakingLoadBalancer) transition(b *cbState, gen uint32, from, next uint64, reason Reason) bool {
	var until int64
	var e int32
	if next == cbOpen {
		e = b.generations.Load() + 1
		until = time.Now().Add(l.openTimeout(e)).UnixNano()
	}

	for {
		v := b.word.Load()
		g, _, state := cbUnpack(v)
		if g != gen || state != from {
			return false // already transitioned out of (gen, from)
		}
		if next == cbOpen {
			b.openedUntil.Store(until) // publish deadline before the state flips to OPEN
		}
		if b.word.CompareAndSwap(v, cbPack(gen+1, 0, next)) {
			b.failures.Store(0)
			b.successes.Store(0)
			switch next {
			case cbOpen:
				b.generations.Store(e)
			case cbClosed:
				b.generations.Store(0)
				b.openedUntil.Store(0)
			}
			l.emit(b, cbExternal(from), cbExternal(next), reason) // one event per generation edge
			return true
		}
		// CAS failed because a sibling changed the probes field at the same
		// (gen, from); re-read. A real transition bumps gen, so the guard above exits.
	}
}

// releaseProbe frees one HALF-OPEN slot held by a sub-threshold successful probe.
// It is generation- and state-guarded: if the generation advanced (a sibling
// tripped or closed, already zeroing the probe count) the CAS cannot match and it
// is a no-op, so a stale probe never under-counts the next generation.
func (l *CircuitBreakingLoadBalancer) releaseProbe(b *cbState, gen uint32) {
	for {
		v := b.word.Load()
		g, probes, state := cbUnpack(v)
		if g != gen || state != cbHalfOpen || probes == 0 {
			return
		}
		if b.word.CompareAndSwap(v, v-cbProbeOne) {
			return
		}
	}
}

// openTimeout returns OpenTimeout doubled for each prior trip, capped at
// MaxOpenTimeout. g is the 1-based generation (trip count).
func (l *CircuitBreakingLoadBalancer) openTimeout(g int32) time.Duration {
	d := l.OpenTimeout
	for i := int32(1); i < g && d < l.MaxOpenTimeout; i++ {
		d *= 2
	}
	if d <= 0 || d > l.MaxOpenTimeout {
		return l.MaxOpenTimeout
	}
	return d
}
