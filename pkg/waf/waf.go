package waf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync/atomic"
	"time"

	"github.com/google/cel-go/cel"
)

// Logger is the interface used by the WAF to emit match events.
// It deliberately avoids a hard dependency on a structured logger package;
// callers can adapt to slog, zap, or stdlib as they prefer.
type Logger interface {
	Logf(format string, args ...any)
}

// LoggerFunc adapts a plain function into the Logger interface.
type LoggerFunc func(format string, args ...any)

// Logf implements Logger.
func (f LoggerFunc) Logf(format string, args ...any) { f(format, args...) }

// MatchEvent is delivered to OnMatch (if set) for every rule that fires.
// It is intentionally lightweight so handlers don't have to copy the request.
//
//nolint:govet
type MatchEvent struct {
	Request *http.Request
	RuleID  string
	Action  Action
	// Status is the rule's configured Status field (defaulted to 403 when
	// the rule didn't set one). It is the HTTP status that *would be*
	// returned for an ActionBlock match; for ActionLog and ActionAllow it
	// carries the configured value but does not describe the actual response.
	Status     int
	Expression string
	ClientIP   string
	// Elapsed is the time spent inside the WAF up to and including this
	// match — i.e. evaluating every rule that ran before this one plus the
	// matching rule itself. It is NOT the per-rule evaluation latency.
	Elapsed time.Duration
}

// Default tunables.
const (
	defaultEvalTimeout  = 5 * time.Millisecond
	defaultCostLimit    = 1_000_000
	defaultBlockStatus  = http.StatusForbidden
	defaultBlockMessage = "Forbidden"
)

// WAF is the middleware. Construct with New.
//
// All fields except the atomic rules pointer should be configured before the
// first request is served. Rules can be replaced at any time via SetRules.
//
//nolint:govet
type WAF struct {
	// Logger receives one line per matched rule. nil disables logging.
	Logger Logger

	// OnMatch is invoked for every rule that fires (any Action). Optional.
	// Runs synchronously on the request goroutine — keep it cheap.
	OnMatch func(MatchEvent)

	// EvalTimeout is the per-request deadline for evaluating the entire
	// ruleset. Defaults to 5ms. A timeout treats the request as Allow
	// (i.e. fails open) but logs the error — this is the safer default for
	// a reverse proxy because failing closed during a config bug would
	// drop legitimate traffic. Override with FailMode to change behaviour.
	EvalTimeout time.Duration

	// CostLimit caps CEL evaluator cost per rule. 0 = use defaultCostLimit.
	// Set explicitly to a smaller number when running untrusted rules.
	CostLimit uint64

	// FailMode controls behaviour when a rule errors at evaluation time
	// (panic recovered, timeout, cost exceeded, type mismatch).
	// Default is FailOpen: error rules are skipped and the request continues.
	FailMode FailMode

	// DisableMacros prevents rules from using all/exists/filter/map/etc.
	// when set to true. Recommended when rules come from less-trusted sources.
	DisableMacros bool

	// InspectBody enables body inspection up to N bytes. 0 = body is empty
	// in expressions (request.body == ""). The buffered body is restored
	// to r.Body so downstream handlers can still read it.
	InspectBody int64

	// rules is an atomic pointer so SetRules can swap the ruleset
	// lock-free; the request path performs only a single atomic load.
	rules atomic.Pointer[ruleset]
}

// FailMode controls behaviour when rule evaluation errors.
type FailMode int

const (
	// FailOpen logs the error and lets the request through. Default.
	FailOpen FailMode = iota
	// FailClosed treats an evaluation error as ActionBlock (status 500).
	FailClosed
)

// ruleset is the immutable bundle of compiled rules swapped into WAF.rules.
type ruleset struct {
	env   *cel.Env
	rules []*compiledRule
}

// New creates a WAF with no rules loaded. Call SetRules before mounting it.
func New() *WAF {
	w := &WAF{}
	w.rules.Store(&ruleset{rules: nil})
	return w
}

// SetRules atomically replaces the ruleset.
//
// It compiles every Rule first and only swaps in the new ruleset if all
// rules compile successfully. This means a bad rule cannot brick the WAF —
// the previous ruleset stays in place and the caller gets an error
// describing every failure.
func (w *WAF) SetRules(rules []Rule) error {
	envOpts := []cel.EnvOption{}
	if w.DisableMacros {
		envOpts = append(envOpts, cel.ClearMacros())
	}
	env, err := newCELEnv(envOpts...)
	if err != nil {
		return fmt.Errorf("waf: build env: %w", err)
	}

	compiled := make([]*compiledRule, 0, len(rules))
	var errs []error
	seen := map[string]struct{}{}
	for i, r := range rules {
		if r.ID == "" {
			errs = append(errs, fmt.Errorf("rule[%d]: missing ID", i))
			continue
		}
		if _, dup := seen[r.ID]; dup {
			errs = append(errs, fmt.Errorf("rule[%d] %q: duplicate ID", i, r.ID))
			continue
		}
		seen[r.ID] = struct{}{}

		if r.Expression == "" {
			errs = append(errs, fmt.Errorf("rule %q: empty expression", r.ID))
			continue
		}

		ast, iss := env.Compile(r.Expression)
		if iss.Err() != nil {
			errs = append(errs, fmt.Errorf("rule %q: compile: %w", r.ID, iss.Err()))
			continue
		}
		if !ast.OutputType().IsExactType(cel.BoolType) {
			errs = append(errs, fmt.Errorf("rule %q: expression must return bool, got %s", r.ID, ast.OutputType()))
			continue
		}
		costLimit := w.CostLimit
		if costLimit == 0 {
			costLimit = defaultCostLimit
		}
		prg, err := env.Program(ast,
			cel.EvalOptions(cel.OptOptimize),
			cel.CostLimit(costLimit),
			cel.InterruptCheckFrequency(64),
		)
		if err != nil {
			errs = append(errs, fmt.Errorf("rule %q: program: %w", r.ID, err))
			continue
		}

		status := r.Status
		if status == 0 {
			status = defaultBlockStatus
		}
		message := r.Message
		if message == "" {
			message = defaultBlockMessage
		}
		compiled = append(compiled, &compiledRule{
			id:          r.ID,
			description: r.Description,
			expression:  r.Expression,
			action:      r.Action,
			status:      status,
			message:     message,
			priority:    r.Priority,
			prg:         prg,
		})
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	// Stable sort by priority so equal priorities preserve declaration order.
	sort.SliceStable(compiled, func(i, j int) bool {
		return compiled[i].priority < compiled[j].priority
	})

	w.rules.Store(&ruleset{env: env, rules: compiled})
	return nil
}

// Rules returns a snapshot of the currently loaded rule IDs in evaluation
// order. Useful for admin endpoints / introspection.
func (w *WAF) Rules() []string {
	rs := w.rules.Load()
	if rs == nil {
		return nil
	}
	out := make([]string, len(rs.rules))
	for i, r := range rs.rules {
		out[i] = r.id
	}
	return out
}

// ServeHandler implements parapet.Middleware.
func (w *WAF) ServeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rs := w.rules.Load()
		if rs == nil || len(rs.rules) == 0 {
			h.ServeHTTP(rw, r)
			return
		}

		var bodyStr string
		if w.InspectBody > 0 && r.Body != nil {
			// LimitReader caps the read so a huge body cannot exhaust memory.
			// We always restore r.Body (even on read error) so downstream sees
			// the bytes we consumed — io.ReadAll returns whatever was read
			// before the error, and dropping that prefix would silently
			// corrupt the request for the next handler.
			lim := io.LimitReader(r.Body, w.InspectBody)
			buf, _ := io.ReadAll(lim)
			bodyStr = string(buf)
			r.Body = bodyReplayCloser{
				Reader: io.MultiReader(bytes.NewReader(buf), r.Body),
				orig:   r.Body,
			}
		}

		input := buildRequestMap(r, bodyStr)

		evalTimeout := w.EvalTimeout
		if evalTimeout <= 0 {
			evalTimeout = defaultEvalTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), evalTimeout)
		defer cancel()

		start := time.Now()
		var ipOnce string
		ip := func() string {
			if ipOnce == "" {
				ipOnce = clientIP(r)
			}
			return ipOnce
		}
		for _, rule := range rs.rules {
			matched, err := evalRule(ctx, rule, input)
			if err != nil {
				if w.Logger != nil {
					w.Logger.Logf("waf: rule %q eval error: %v", rule.id, err)
				}
				if w.FailMode == FailClosed {
					http.Error(rw, "WAF Error", http.StatusInternalServerError)
					return
				}
				continue
			}
			if !matched {
				continue
			}

			if w.OnMatch != nil {
				w.OnMatch(MatchEvent{
					Request:    r,
					RuleID:     rule.id,
					Action:     rule.action,
					Status:     rule.status,
					Expression: rule.expression,
					ClientIP:   ip(),
					Elapsed:    time.Since(start),
				})
			}
			if w.Logger != nil {
				w.Logger.Logf("waf: matched rule=%q action=%s status=%d ip=%s method=%s host=%s path=%s",
					rule.id, rule.action, rule.status, ip(), r.Method, r.Host, r.URL.Path)
			}

			switch rule.action {
			case ActionAllow:
				h.ServeHTTP(rw, r)
				return
			case ActionBlock:
				http.Error(rw, rule.message, rule.status)
				return
			case ActionLog:
				// continue evaluating remaining rules
			}
		}

		h.ServeHTTP(rw, r)
	})
}

// evalRule runs a single CEL program with panic recovery. Panics inside CEL
// are not expected but a defensive recover ensures one buggy custom function
// can never take down the proxy.
func evalRule(ctx context.Context, rule *compiledRule, input map[string]any) (matched bool, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
		}
	}()
	out, _, evErr := rule.prg.ContextEval(ctx, input)
	if evErr != nil {
		return false, evErr
	}
	if out == nil {
		return false, nil
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("expected bool, got %T", out.Value())
	}
	return b, nil
}

// bodyReplayCloser stitches a buffered prefix back onto the live body so
// downstream handlers see the original bytes.
type bodyReplayCloser struct {
	io.Reader
	orig io.ReadCloser
}

func (b bodyReplayCloser) Close() error { return b.orig.Close() }
