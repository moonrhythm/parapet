# pkg/waf — implementation notes

This file records what worked, what didn't, and the surprises encountered
while building the WAF on top of `github.com/google/cel-go`. Future changes
should append rather than rewrite — the value is in the historical context.

## Design decisions that landed

- **Atomic ruleset swap (`atomic.Pointer[ruleset]`)** for hot reload. The
  request path performs a single `Load()`, no locks, no allocation. Old
  in-flight requests continue using the previous ruleset until they
  complete; new requests pick up the new ruleset on the next call. This is
  the same pattern Caddy and Envoy use for live config updates.

- **All-or-nothing compilation in `SetRules`**. If any rule fails to compile
  (syntax, type-check, or non-bool result), the entire batch is rejected
  and the previous ruleset stays in place. This means an admin pushing a
  bad rule cannot brick the proxy.

- **Lazy regex/CIDR caches** keyed by the pattern string. CEL `OptOptimize`
  pre-folds calls to the built-in `string.matches` when the pattern is a
  literal, but our `regexMatch` custom function gets called every eval, so
  caching at the Go level closes that gap. `sync.Map` is fine because the
  cache is read-mostly after warm-up.

- **Fail-open by default**. If a rule errors at runtime (bad regex pattern
  from a header, cost-limit exceeded, panic in custom function), the
  request is logged and forwarded. For a proxy in the request path, the
  cost of fail-closed during a config bug (every request 500s) is much
  worse than the cost of fail-open (one rule briefly inactive). Operators
  who need fail-closed can opt in via `WAF.FailMode`.

- **`request` map exposed to CEL with snake_case keys** to mirror common
  WAF conventions (ModSecurity, Coraza). Headers are lowercased so rule
  authors don't have to remember canonical casing.

## What did NOT work / surprises

- **`OptOptimize` constant-folding bites you twice**:
  1. The first iteration of `TestFailOpenIsDefault` passed a literal bad
     regex `(?P<a>x` to `string.matches`. CEL folded it at compile time
     and `SetRules` returned an error rather than the rule deferring the
     failure to eval. Fix: drive the regex through an input header so it
     can't be folded.
  2. `TestEvalTimeout` originally used `[1, 2, 3, …, 10].all(x, x > 0)`.
     Constant list + constant comparison → fully folded, zero runtime
     cost. Cost limit never triggers. Fix: route the rule through
     `request.path == request.method` and set CostLimit to 1.

- **`cel.CostLimit` only counts CEL-level operations**, not work performed
  inside Go-implemented custom functions (`regexMatch`, `ipInCidr`, etc.).
  A regex with catastrophic backtracking inside our `regexMatch` would not
  trip the cost ceiling because the cost is invisible to the interpreter.
  Mitigations:
  - Go's `regexp` package is RE2 → no catastrophic backtracking by
    construction, so this is mostly fine.
  - The `EvalTimeout` ContextEval path *does* fire because
    `prg.ContextEval(ctx, …)` honours `ctx.Done()` between iterations.
  - If we ever swap to a different regex engine, we must bound the per-call
    deadline ourselves.

- **Querystring normalisation**: `?q=1+UNION+SELECT+pass` parses with `+`
  characters in `request.query` because `r.URL.RawQuery` is not decoded.
  Rule authors must apply `urlDecode(request.query)` themselves. We
  exposed `urlDecode()` as a CEL function for exactly this reason; without
  it, regex rules will silently miss URL-encoded payloads.

- **CEL list iteration**: extracting `[]string` from a `ref.Val` requires
  `traits.Lister` from `github.com/google/cel-go/common/types/traits` and
  iteration via `lister.Iterator()`. The first attempt used a naive
  custom interface with `Get(types.Int)` which is *not* the right
  signature — `Lister.Get` takes `ref.Val`. Iterator avoids that issue
  entirely.

- **Map type for `request`**: declared as
  `cel.MapType(cel.StringType, cel.DynType)`. `DynType` defeats CEL's
  static type checker — a typo like `request.metohd` compiles cleanly
  and silently returns null. The trade-off is worth it because it lets
  us add new fields without breaking rule schemas; for a stricter
  alternative we'd need a proto definition or a custom `types.Provider`.

- **Body inspection memory amplification**. The first attempt buffered
  the entire body into RAM. Replaced with an explicit `WAF.InspectBody`
  byte cap (default 0 = disabled) and an `io.LimitReader` so that even a
  misconfigured rule on a multi-GB upload cannot OOM the proxy.

- **`io.MultiReader(byteReader(buf), r.Body)`** was the original body
  replay trick using a `[]byte`-as-Reader. It's a footgun because
  `MultiReader` keeps reading the first reader until EOF, but a stateless
  byte slice can't return EOF. Replaced with `bytes.NewReader(buf)` which
  is stateful and correct. Lesson: don't reinvent `bytes.NewReader`.

## Things to try later

- **`cel.HomogeneousAggregateLiterals()`** to refuse `[1, "a", true]`
  literal lists at compile time. Currently allowed; not a security risk
  but a footgun.

- **Pre-compile `cel.Macros(...)` removal as the default** when an explicit
  `WAF.UntrustedRules` flag is set, so `all`/`exists`/`map`/`filter` aren't
  even available. Right now this is gated by `DisableMacros` and defaults
  to false because most operator-authored rules want comprehensions.

- **Per-rule cost & latency metrics** via the `OnMatch` hook plus a
  parallel "rule executed" hook so admins can spot a single hot rule.

- **Bytecode cache across `SetRules` calls**: when 99% of rules don't
  change, recompiling all of them is wasteful. A content-addressed cache
  keyed by the expression string would skip compilation for unchanged
  rules. Not done because compile cost is paid out-of-band, not on the
  request path, so the win is small.

- **Distributed counters** (rule fire rate, request rate by client IP) so
  rules can express things like "block this IP if rule X fires more than
  N times in 60s". Today rules are stateless; this would need a counter
  abstraction that survives hot reload.

## Performance baseline (Apple M-class proxy, `go test -bench`, 4 cores)

Numbers from the local sandbox (linux/arm64); your mileage will vary but
the relative shape is what matters:

```
BenchmarkNoRules-4              111 ns/op      4 allocs/op   (middleware floor)
BenchmarkSingleSimpleRule-4    1.9 µs/op     36 allocs/op
BenchmarkRegexRule-4           2.6 µs/op     43 allocs/op
BenchmarkTenRules-4            7.2 µs/op    147 allocs/op
BenchmarkParallel100Rules-4     39 µs/op   1526 allocs/op
BenchmarkSetRules-4            137 µs/op   1433 allocs/op   (off the request path)
```

Hot spots seen in the alloc profile:
- `buildRequestMap` allocates a map + per-header copies. Could be reduced
  by lazily populating fields or returning a `types.Provider` that walks
  the request on demand. Deferred until profiling on real traffic shows
  it matters.
- `httptest.NewRecorder()` itself allocates ~16 of the per-call allocs in
  the benchmarks; production traffic with a real `http.ResponseWriter`
  will have a slightly lower floor.
