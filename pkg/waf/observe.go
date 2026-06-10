package waf

import (
	"net/http"
	"time"
)

// Outcome classifies the terminal disposition of one WAF evaluation, reported
// once per request via WAF.Observe regardless of whether any rule matched. It is
// a small, bounded set so it is safe as a Prometheus label; it is NEVER the rule
// ID (which is unbounded and lives on MatchEvent.RuleID instead).
type Outcome uint8

const (
	// OutcomePass: evaluation finished without a terminating match and the
	// request was forwarded to the next handler. This is the common case and
	// also subsumes ActionLog-only matches (which never terminate) and any
	// FailOpen-swallowed rule errors along the way (which also do not
	// terminate). It is the silent-majority path OnMatch cannot see.
	OutcomePass Outcome = iota
	// OutcomeAllow: an ActionAllow rule fired; evaluation short-circuited and
	// the request was forwarded without running the remaining rules.
	OutcomeAllow
	// OutcomeBlock: an ActionBlock rule fired; the request was rejected with
	// the rule's status.
	OutcomeBlock
	// OutcomeError: a rule errored (panic recovered, timeout, cost exceeded,
	// type mismatch) under FailMode=FailClosed, terminating the request with
	// 500. Under the default FailOpen a rule error is swallowed and the request
	// continues, so it is NOT OutcomeError — it folds into the eventual
	// Pass/Allow/Block outcome. An EvalTimeout firing is just such an eval
	// error, so "timeout" is not a distinct outcome.
	OutcomeError
)

// String implements fmt.Stringer; the returned values are exactly the
// Prometheus label values used by prom.WAF.
func (o Outcome) String() string {
	switch o {
	case OutcomePass:
		return "pass"
	case OutcomeAllow:
		return "allow"
	case OutcomeBlock:
		return "block"
	case OutcomeError:
		return "error"
	default:
		return "unknown"
	}
}

// EvalEvent is delivered to WAF.Observe (if set) exactly ONCE per request that
// reaches rule evaluation, after evaluation terminates, regardless of outcome.
// Unlike MatchEvent (which fires per matched rule and only on a match), it makes
// the WAF's per-request overhead visible on EVERY path — including the common
// no-match fall-through and the allow/error short-circuits.
//
//nolint:govet // fields ordered for readability, not alignment
type EvalEvent struct {
	// Request is the request that was evaluated. The hook may read it (e.g. to
	// derive a custom label) but must not mutate it; prom.WAF ignores it.
	Request *http.Request
	// Outcome is how evaluation terminated. Bounded; safe as a metric label.
	Outcome Outcome
	// Duration is the wall time spent evaluating rules — the SAME span as
	// MatchEvent.Elapsed. It is time.Since(start) where start is taken AFTER
	// body buffering, Country/ASN lookup and request-map construction, measured
	// at the terminating decision point BEFORE the request is handed to the next
	// handler. It therefore EXCLUDES client body I/O, geo/ASN/map-build cost, and
	// downstream-handler latency, and measures rule evaluation only.
	Duration time.Duration
}

// ObserveFunc is the per-request evaluation-observation hook shape, returned by
// prom.WAF for wiring into WAF.Observe (the prom.Mirror / prom.Cache
// convention), keeping pkg/waf free of any Prometheus dependency.
type ObserveFunc func(EvalEvent)
