package waf

// Action describes what the WAF should do when a Rule matches.
type Action int

const (
	// ActionLog records the match (via WAF.Logger) and lets the request continue.
	// Useful for shadow-deploying new rules.
	ActionLog Action = iota

	// ActionAllow short-circuits the WAF chain: no further rules are evaluated
	// and the request is forwarded to the next handler. This is the inverse of
	// ActionBlock and is intended for explicit allowlists (e.g. trusted health
	// checkers, internal scanners).
	ActionAllow

	// ActionBlock terminates the request with the rule's Status (defaults to 403).
	ActionBlock
)

// String implements fmt.Stringer for log readability.
func (a Action) String() string {
	switch a {
	case ActionLog:
		return "log"
	case ActionAllow:
		return "allow"
	case ActionBlock:
		return "block"
	default:
		return "unknown"
	}
}
