package waf

import (
	"github.com/google/cel-go/cel"
)

// Rule is a single WAF rule definition.
//
// Rules are sorted by ascending Priority (smaller runs first) when SetRules
// is called. Within equal priorities, declaration order is preserved.
type Rule struct {
	// ID is a stable identifier used in logs. Required.
	ID string

	// Description is an optional human-readable summary recorded in match logs.
	Description string

	// Expression is a CEL expression that must evaluate to bool.
	// See package docs for the available variables and functions.
	Expression string

	// Action determines what to do when the expression returns true.
	Action Action

	// Status is the HTTP status returned for ActionBlock matches.
	// Defaults to 403 Forbidden when zero.
	Status int

	// Message is the response body for ActionBlock matches. Defaults to a
	// generic "Forbidden" string. Returned as text/plain.
	Message string

	// Priority controls evaluation order; lower values run first.
	// Allowlist rules should typically have the smallest Priority so they
	// short-circuit before any block rules are considered.
	Priority int
}

// compiledRule is the internal representation kept inside an atomic ruleset.
type compiledRule struct {
	id          string
	description string
	expression  string
	action      Action
	status      int
	message     string
	priority    int
	prg         cel.Program
}
