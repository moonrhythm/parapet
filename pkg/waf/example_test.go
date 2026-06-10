package waf_test

import (
	"log"
	"net/http"
	"time"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/waf"
)

// Mount a WAF on an edge-facing server: construct it, load a CEL ruleset that
// blocks obvious SQL-injection attempts, then wire it ahead of the upstream.
func ExampleNew() {
	w := waf.New()
	_ = w.SetRules([]waf.Rule{{
		ID:         "block-sqli",
		Expression: `request.query.contains("' OR '1'='1") || request.path.matches("(?i).*union.*select.*")`,
		Action:     waf.ActionBlock,
		Status:     http.StatusForbidden,
	}})

	s := parapet.NewFrontend()
	s.Use(w)
	// s.Use(upstream.SingleHost("10.0.0.1:8080")) — the protected backend.
}

// Allowlist rules short-circuit before any block rule runs. Give them the
// smallest Priority so trusted clients (here, an internal scanner) are waved
// through, while a lower-priority block rule rejects everyone else from a
// sensitive path.
func ExampleWAF_SetRules_allowlist() {
	w := waf.New()
	_ = w.SetRules([]waf.Rule{
		{
			ID:         "allow-internal-scanner",
			Priority:   0, // runs first; ActionAllow ends evaluation
			Expression: `ipInCidr(request.remote_ip, "10.0.0.0/8")`,
			Action:     waf.ActionAllow,
		},
		{
			ID:         "block-admin-from-outside",
			Priority:   10,
			Expression: `hasPrefixAny(request.path, ["/admin", "/internal"])`,
			Action:     waf.ActionBlock,
			Status:     http.StatusForbidden,
			Message:    "admin access denied",
		},
	})

	s := parapet.NewFrontend()
	s.Use(w)
}

// Shadow-deploy a new rule with ActionLog: matches are recorded via the Logger
// (and OnMatch) but the request still passes, so you can measure a rule's hit
// rate before switching it to ActionBlock.
func ExampleWAF_shadow() {
	w := waf.New()
	w.Logger = waf.LoggerFunc(log.Printf)
	w.OnMatch = func(ev waf.MatchEvent) {
		// e.g. increment a metric keyed by ev.RuleID / ev.Action.
		_ = ev
	}
	_ = w.SetRules([]waf.Rule{{
		ID:         "candidate-bad-ua",
		Expression: `lower(request.user_agent).contains("sqlmap")`,
		Action:     waf.ActionLog, // observe only; does not block
	}})

	s := parapet.NewFrontend()
	s.Use(w)
}

// Enable request-body inspection and tighten the evaluation limits. Body
// inspection is off by default; set InspectBody to buffer up to N bytes so
// rules can match on request.body. EvalTimeout and CostLimit bound the cost of
// each request, and FailClosed rejects a request whose rules error instead of
// failing open.
func ExampleWAF_inspectBody() {
	w := waf.New()
	w.InspectBody = 64 << 10 // buffer up to 64 KiB of the body
	w.EvalTimeout = 2 * time.Millisecond
	w.CostLimit = 500_000
	w.FailMode = waf.FailClosed
	_ = w.SetRules([]waf.Rule{{
		ID:         "block-body-script-tag",
		Expression: `request.method == "POST" && lower(request.body).contains("<script")`,
		Action:     waf.ActionBlock,
	}})

	s := parapet.NewFrontend()
	s.Use(w)
}

// Filter by GeoIP country and ASN. The WAF stays storage-agnostic: supply the
// lookups (a GeoIP/IP-to-ASN database, an edge header, etc.) and reference the
// resolved values as request.country and request.asn in expressions.
func ExampleWAF_geo() {
	w := waf.New()
	w.Country = func(r *http.Request) string {
		// e.g. resolve from a MaxMind DB or trust an edge header.
		return r.Header.Get("CF-IPCountry")
	}
	w.ASN = func(r *http.Request) int64 {
		return 0 // e.g. resolve from an IP-to-ASN database.
	}
	_ = w.SetRules([]waf.Rule{{
		ID:         "block-country-and-asn",
		Expression: `request.country == "XX" || request.asn == 13335`,
		Action:     waf.ActionBlock,
	}})

	s := parapet.NewFrontend()
	s.Use(w)
}

// Observe fires once per evaluated request regardless of outcome, so it can
// answer "how much is the WAF costing me" on the common no-match path that
// OnMatch never sees. Wire prom.WAF() for a histogram, or a custom hook.
func ExampleWAF_observe() {
	w := waf.New()
	_ = w.SetRules([]waf.Rule{{
		ID:         "block-sqli",
		Expression: `request.query.contains("' OR '1'='1")`,
		Action:     waf.ActionBlock,
	}})

	w.Observe = func(ev waf.EvalEvent) {
		// Fires on pass/allow/block/error alike; ev.Duration is rule-eval time.
		log.Printf("waf eval outcome=%s took=%s", ev.Outcome, ev.Duration)
	}

	s := parapet.NewFrontend()
	s.Use(w)
}
