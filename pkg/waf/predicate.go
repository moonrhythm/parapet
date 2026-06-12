package waf

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/cel-go/cel"
)

// Predicate is a standalone compiled CEL boolean expression over the SAME
// `request` model and helper functions as WAF rules (see package docs for the
// variables and functions). It exists so other middleware can gate behaviour on
// request attributes using the WAF's CEL surface without duplicating — or
// drifting from — the environment. The canonical consumer is rate limiting,
// which uses a Predicate to decide whether a limit applies to a request.
//
// A Predicate is compiled once by NewPredicate and is safe for concurrent Eval.
// Unlike a Rule it carries no action/status/priority: it answers one question,
// "does this request match", and the caller decides what to do with the answer.
type Predicate struct {
	prg         cel.Program // interface (2 ptr words); first to keep the GC scan span minimal (fieldalignment)
	expression  string
	evalTimeout time.Duration
}

// predicateConfig holds the resolved options for NewPredicate.
type predicateConfig struct {
	costLimit     uint64
	evalTimeout   time.Duration
	disableMacros bool
}

// PredicateOption configures NewPredicate. The defaults match the WAF's own
// rule compilation (cost limit defaultCostLimit, eval timeout defaultEvalTimeout,
// macros enabled), so a predicate is bounded exactly like a rule unless the
// caller tightens it.
type PredicateOption func(*predicateConfig)

// WithPredicateCostLimit caps CEL evaluator cost per Eval (0 ⇒ defaultCostLimit).
func WithPredicateCostLimit(n uint64) PredicateOption {
	return func(c *predicateConfig) { c.costLimit = n }
}

// WithPredicateEvalTimeout sets the per-Eval deadline (<=0 ⇒ defaultEvalTimeout).
func WithPredicateEvalTimeout(d time.Duration) PredicateOption {
	return func(c *predicateConfig) { c.evalTimeout = d }
}

// WithPredicateDisableMacros refuses expressions that use CEL macros
// (all/exists/filter/map/comprehensions) — recommended when the expression
// comes from a less-trusted source, mirroring WAF.DisableMacros.
func WithPredicateDisableMacros() PredicateOption {
	return func(c *predicateConfig) { c.disableMacros = true }
}

// NewPredicate compiles expr into a Predicate using the WAF's CEL environment.
// expr must be non-empty and evaluate to bool; a compile error, a non-bool
// result type, or a program-build error is returned (the same checks SetRules
// applies to a rule expression), so an invalid predicate is caught at
// construction, never at request time.
func NewPredicate(expr string, opts ...PredicateOption) (*Predicate, error) {
	if expr == "" {
		return nil, fmt.Errorf("waf: predicate: empty expression")
	}

	cfg := predicateConfig{
		costLimit:   defaultCostLimit,
		evalTimeout: defaultEvalTimeout,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.costLimit == 0 {
		cfg.costLimit = defaultCostLimit
	}
	if cfg.evalTimeout <= 0 {
		cfg.evalTimeout = defaultEvalTimeout
	}

	var envOpts []cel.EnvOption
	if cfg.disableMacros {
		envOpts = append(envOpts, cel.ClearMacros())
	}
	env, err := newCELEnv(envOpts...)
	if err != nil {
		return nil, fmt.Errorf("waf: predicate: build env: %w", err)
	}

	ast, iss := env.Compile(expr)
	if iss.Err() != nil {
		return nil, fmt.Errorf("waf: predicate: compile: %w", iss.Err())
	}
	if !ast.OutputType().IsExactType(cel.BoolType) {
		return nil, fmt.Errorf("waf: predicate: expression must return bool, got %s", ast.OutputType())
	}
	prg, err := env.Program(ast,
		cel.EvalOptions(cel.OptOptimize),
		cel.CostLimit(cfg.costLimit),
		cel.InterruptCheckFrequency(64),
	)
	if err != nil {
		return nil, fmt.Errorf("waf: predicate: program: %w", err)
	}

	return &Predicate{
		expression:  expr,
		prg:         prg,
		evalTimeout: cfg.evalTimeout,
	}, nil
}

// Expression returns the source expression, for logs and introspection.
func (p *Predicate) Expression() string { return p.expression }

// Input is a materialised `request` snapshot for one HTTP request. Build it once
// with NewInput and reuse it across several Predicate.Eval calls so the request
// (headers, cookies, query) is walked only once per request, however many
// predicates evaluate against it. It is read-only; do not mutate after building.
type Input struct {
	m map[string]any
}

// NewInput materialises the `request` snapshot for r, exposing exactly the
// fields documented for WAF rules. body is the (caller-truncated) string for
// request.body — pass "" to leave it empty (no body inspection). country/asn are
// the resolved GeoIP values for request.country / request.asn — pass "" / 0 when
// unresolved or unused; the fields are always present so an expression
// referencing them never errors.
func NewInput(r *http.Request, body, country string, asn int64) Input {
	return Input{m: buildRequestMap(r, body, country, asn)}
}

// Eval evaluates the predicate against in and returns the boolean result. It
// applies the predicate's own eval timeout (derived from ctx) and cost limit; a
// timeout, cost-limit breach, runtime type error, or recovered panic is returned
// as a non-nil error with a false result, leaving the fail-open/fail-closed
// decision to the caller. ctx may carry the request's cancellation.
func (p *Predicate) Eval(ctx context.Context, in Input) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, p.evalTimeout)
	defer cancel()
	return evalProgram(ctx, p.prg, in.m)
}
