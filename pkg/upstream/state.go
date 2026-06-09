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
	ReasonTrip    Reason = iota // closed -> open: failure threshold crossed (circuit breaker)
	ReasonReopen                // half_open -> open: a probe failed (circuit breaker)
	ReasonHeal                  // half_open -> closed: success threshold met (circuit breaker)
	ReasonProbe                 // open -> half_open: cooldown expired, probing (circuit breaker)
	ReasonExpire                // half_open -> open: a probe overstayed ProbeTimeout (circuit breaker)
	ReasonEject                 // -> open: an ejecting balancer took a target out of rotation
	ReasonRecover               // open -> closed: an ejecting balancer returned a target to rotation
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
	default:
		return "trip"
	}
}

// StateChange reports one per-target reliability transition to a StateChangeFunc.
type StateChange struct {
	Host   string // resolved upstream target (operator-configured, bounded label)
	From   State
	To     State
	Reason Reason
}

// StateChangeFunc observes a per-target reliability state transition. Assign one to
// a reliability balancer's OnStateChange to make ejection / circuit-breaker state
// observable — see prom.UpstreamState. It is invoked synchronously on the goroutine
// that commits the transition, exactly once per transition (concurrent threshold
// crossers collapse to one), after the new state is published. The callee owns its
// own concurrency. Nil disables it at zero hot-path cost.
type StateChangeFunc func(StateChange)
