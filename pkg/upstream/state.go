package upstream

// State is a reliability balancer's per-target health state, reported via
// OnStateChange. The circuit breaker uses all three; EjectingLoadBalancer and
// LatencyEjectingLoadBalancer use only StateClosed (in rotation) and StateOpen
// (ejected). The numeric value doubles as the Prometheus gauge value exported by
// prom.UpstreamState.
type State uint8

const (
	StateClosed   State = iota // routing normally / in rotation
	StateOpen                  // tripped open / ejected — skipped
	StateHalfOpen              // admitting trial probes (circuit breaker only)
)

func (s State) String() string {
	switch s {
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "closed"
	}
}

// Reason names what drove a transition, for metric labelling.
type Reason uint8

const (
	ReasonTrip         Reason = iota // closed -> open: failure threshold crossed (circuit breaker)
	ReasonReopen                     // half_open -> open: a probe failed (circuit breaker)
	ReasonHeal                       // half_open -> closed: success threshold met (circuit breaker)
	ReasonProbe                      // open -> half_open: cooldown expired, probing (circuit breaker)
	ReasonExpire                     // half_open -> open: a probe overstayed ProbeTimeout (circuit breaker)
	ReasonEject                      // -> open: an ejecting balancer took a target out of rotation
	ReasonRecover                    // open -> closed: an ejecting balancer returned a target to rotation
	ReasonProbeDown                  // closed -> open: an active health probe failed UnhealthyThld times in a row
	ReasonProbeRecover               // open -> closed: an active health probe succeeded HealthyThld times in a row
)

func (r Reason) String() string {
	switch r {
	case ReasonReopen:
		return "reopen"
	case ReasonHeal:
		return "heal"
	case ReasonProbe:
		return "probe"
	case ReasonExpire:
		return "expire"
	case ReasonEject:
		return "eject"
	case ReasonRecover:
		return "recover"
	case ReasonProbeDown:
		return "probe_down"
	case ReasonProbeRecover:
		return "probe_recover"
	default:
		return "trip"
	}
}

// ProbeCause classifies WHY an ActiveHealthCheck probe failed. It is carried on a
// ReasonProbeDown StateChange (StateChange.Cause) so an operator can tell a bad
// probe path from a dead backend from a too-tight Timeout mid-incident. It is a
// CLOSED set — a bounded Prometheus label, never unbounded. The zero value,
// CauseNone, means "not a probe-down event": every non-ActiveHealthCheck emitter
// (circuit breaker, ejecting, latency-ejecting) leaves it zero, and a probe
// RECOVER carries CauseNone too. A transport error matching none of the specific
// cases collapses into CauseError, so the label set can never grow.
type ProbeCause uint8

const (
	CauseNone    ProbeCause = iota // not a probe-down event (the zero value)
	CauseTimeout                   // per-probe Timeout deadline fired (context.DeadlineExceeded / net.Error.Timeout)
	CauseRefused                   // connection refused (syscall.ECONNREFUSED): nothing listening
	CauseReset                     // connection reset / closed mid-probe (syscall.ECONNRESET, io.EOF, io.ErrUnexpectedEOF)
	CauseDNS                       // name resolution failed (*net.DNSError)
	CauseTLS                       // TLS handshake / certificate failure (tls.RecordHeaderError, *tls.CertificateVerificationError)
	CauseStatus                    // a response arrived but healthy() rejected it (e.g. status >= 400)
	CauseError                     // any other transport error (catch-all, keeps the set closed)
)

func (c ProbeCause) String() string {
	switch c {
	case CauseTimeout:
		return "timeout"
	case CauseRefused:
		return "refused"
	case CauseReset:
		return "reset"
	case CauseDNS:
		return "dns"
	case CauseTLS:
		return "tls"
	case CauseStatus:
		return "status"
	case CauseError:
		return "error"
	default:
		return "none"
	}
}

// StateChange reports one per-target reliability transition to a StateChangeFunc.
type StateChange struct {
	Host   string // resolved upstream target (operator-configured, bounded label)
	From   State
	To     State
	Reason Reason
	// Cause classifies a probe failure on the ActiveHealthCheck down crossing
	// (Reason == ReasonProbeDown). It is CauseNone (the zero value) on every other
	// event, including a probe recover and all circuit-breaker / ejecting transitions.
	Cause ProbeCause
}

// StateChangeFunc observes a per-target reliability state transition. Assign one to
// a reliability balancer's OnStateChange to make ejection / circuit-breaker state
// observable — see prom.UpstreamState. It is invoked synchronously on the goroutine
// that commits the transition, exactly once per transition (concurrent threshold
// crossers collapse to one), after the new state is published. The callee owns its
// own concurrency. Nil disables it at zero hot-path cost.
type StateChangeFunc func(StateChange)
