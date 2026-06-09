# parapet

![Build Status](https://github.com/moonrhythm/parapet/actions/workflows/test.yaml/badge.svg?branch=master)
[![Go Report Card](https://goreportcard.com/badge/github.com/moonrhythm/parapet)](https://goreportcard.com/report/github.com/moonrhythm/parapet)
[![GoDoc](https://godoc.org/github.com/moonrhythm/parapet?status.svg)](https://godoc.org/github.com/moonrhythm/parapet)

A composable reverse proxy framework for Go. Parapet is a library, not a binary: you build your edge or backend by importing the pieces you need and chaining them together with `Use`.

## Install

```sh
go get github.com/moonrhythm/parapet
```

Requires Go 1.25 or later.

## Concepts

- **`Middleware`** — anything that satisfies `ServeHandler(http.Handler) http.Handler`. Every feature in this library is a `Middleware`.
- **`Middlewares`** — an ordered slice of `Middleware`, applied in reverse order so the first `Use` call sits outermost (like an onion).
- **`Server`** — wraps `http.Server` with a middleware chain and adds TLS, H2C, graceful shutdown (30 s grace, 10 s wait), and reuseport.

Three constructors pick sensible defaults for the role the server plays:

| Constructor | Use for | Notable defaults |
|---|---|---|
| `parapet.New()` | General purpose, behind another proxy | Trusts standard private CIDRs, long idle timeout |
| `parapet.NewFrontend()` | Edge / internet-facing | Read/write/header timeouts, no trusted proxies |
| `parapet.NewBackend()` | Internal service behind parapet | H2C enabled, trusts private CIDRs |

## Packages

Each subdirectory under `pkg/` is a self-contained middleware:

| Package | What it does |
|---|---|
| [`upstream`](pkg/upstream) | Reverse proxy and load balancing (round-robin, or round-robin with passive health checks) over HTTP, H2C, or HTTPS |
| [`host`](pkg/host) | Virtual-host routing on the `Host` header, with wildcard prefixes |
| [`location`](pkg/location) | Path routing — exact, prefix, and regexp matchers |
| [`router`](pkg/router) | Simple URL router |
| [`block`](pkg/block) | Conditional middleware container — match a request, then apply an inner chain |
| [`mirror`](pkg/mirror) | Traffic shadowing — tee a copy of matched/sampled requests to a canary, fire-and-forget |
| [`ratelimit`](pkg/ratelimit) | Fixed-window, sliding-window, concurrent, and leaky-bucket limiters |
| [`compress`](pkg/compress) | Content-negotiated compression (Gzip, Brotli, Deflate) |
| [`cache`](pkg/cache) | HTTP response cache — honor-origin policy, in-memory or disk backend, single-flight fills, `X-Cache` tag |
| [`cache/purge`](pkg/cache/purge) | Cache invalidation — purge by host, URL, path prefix, or surrogate tag, plus a reaper |
| [`body`](pkg/body) | Request body limiting and buffering |
| [`headers`](pkg/headers) | Request/response header manipulation |
| [`cors`](pkg/cors) | CORS handling |
| [`hsts`](pkg/hsts) | `Strict-Transport-Security` (with preload) |
| [`redirect`](pkg/redirect) | HTTPS, www/non-www, and arbitrary redirects |
| [`requestid`](pkg/requestid) | Inject and propagate a request ID |
| [`logger`](pkg/logger) | Structured request logging |
| [`healthz`](pkg/healthz) | Health-check endpoint |
| [`timeout`](pkg/timeout) | Per-request deadline enforcement |
| [`fileserver`](pkg/fileserver) | Static file serving |
| [`stripprefix`](pkg/stripprefix) | Strip a URL path prefix before proxying |
| [`authn`](pkg/authn) | JWT and basic-auth helpers |
| [`waf`](pkg/waf) | Web application firewall driven by CEL expressions, hot reloadable |
| [`prom`](pkg/prom) | Prometheus metrics |
| [`proxyprotocol`](pkg/proxyprotocol) | HAProxy PROXY protocol (v1/v2) — recover the real client IP behind an L4 load balancer |
| [`h2push`](pkg/h2push) | HTTP/2 server push helpers |
| [`gcp`](pkg/gcp), [`gcs`](pkg/gcs), [`stackdriver`](pkg/stackdriver), [`trace`](pkg/trace) | Google Cloud integrations and distributed tracing |

## Example

```go
package main

import (
	"log"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/body"
	"github.com/moonrhythm/parapet/pkg/compress"
	"github.com/moonrhythm/parapet/pkg/headers"
	"github.com/moonrhythm/parapet/pkg/healthz"
	"github.com/moonrhythm/parapet/pkg/host"
	"github.com/moonrhythm/parapet/pkg/hsts"
	"github.com/moonrhythm/parapet/pkg/location"
	"github.com/moonrhythm/parapet/pkg/logger"
	"github.com/moonrhythm/parapet/pkg/ratelimit"
	"github.com/moonrhythm/parapet/pkg/redirect"
	"github.com/moonrhythm/parapet/pkg/requestid"
	"github.com/moonrhythm/parapet/pkg/upstream"
)

func main() {
	s := parapet.New()
	s.Use(logger.Stdout())
	s.Use(requestid.New())
	s.Use(ratelimit.FixedWindowPerSecond(60))
	s.Use(ratelimit.FixedWindowPerMinute(300))
	s.Use(ratelimit.FixedWindowPerHour(2000))
	s.Use(body.LimitRequest(15 * 1024 * 1024)) // 15 MiB
	s.Use(body.BufferRequest())
	s.Use(compress.Gzip())
	s.Use(compress.Br())

	// sites
	s.Use(example())
	s.Use(mysite())
	s.Use(wordpress())

	// health check
	{
		l := location.Exact("/healthz")
		l.Use(logger.Disable())
		l.Use(healthz.New())
		s.Use(l)
	}

	s.Addr = ":8080"
	if err := s.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func example() parapet.Middleware {
	h := host.New("example.com", "www.example.com")
	h.Use(ratelimit.FixedWindowPerSecond(20))
	h.Use(redirect.HTTPS())
	h.Use(hsts.Preload())
	h.Use(redirect.NonWWW())
	h.Use(upstream.New(upstream.NewRoundRobinLoadBalancer([]*upstream.Target{
		{Host: "example.default.svc.cluster.local:8080", Transport: &upstream.HTTPTransport{}},
		{Host: "example1.default.svc.cluster.local:8080", Transport: &upstream.H2CTransport{}},
		{Host: "myexamplebackuphost.com", Transport: &upstream.HTTPSTransport{}},
	})))
	return h
}

func mysite() parapet.Middleware {
	var hs parapet.Middlewares

	{
		h := host.New("mysiteaaa.io", "www.mysiteaaa.io")
		h.Use(ratelimit.FixedWindowPerSecond(15))
		h.Use(redirect.HTTPS())
		h.Use(hsts.Preload())
		h.Use(redirect.WWW())
		h.Use(headers.DeleteResponse(
			"Server",
			"x-goog-generation",
			"x-goog-hash",
			"x-goog-meta-goog-reserved-file-mtime",
			"x-goog-metageneration",
			"x-goog-storage-class",
			"x-goog-stored-content-encoding",
			"x-goog-stored-content-length",
			"x-guploader-uploadid",
		))
		h.Use(upstream.SingleHost("storage.googleapis.com", &upstream.HTTPSTransport{}))
		hs.Use(h)
	}

	{
		h := host.New("mail.mysiteaaa.io")
		h.Use(redirect.HTTPS())
		h.Use(hsts.Preload())
		h.Use(redirect.To("https://mail.google.com/a/mysiteaaa.io", 302))
		hs.Use(h)
	}

	return hs
}

func wordpress() parapet.Middleware {
	h := host.New("myblogaaa.com", "www.myblogaaa.com")
	h.Use(ratelimit.FixedWindowPerMinute(150))
	h.Use(redirect.HTTPS())
	h.Use(hsts.Preload())
	h.Use(redirect.NonWWW())

	backend := upstream.SingleHost("wordpress.default.svc.cluster.local", &upstream.HTTPTransport{})

	l := location.RegExp(`\.(js|css|svg|png|jp(e)?g|gif)$`)
	l.Use(headers.SetResponse("Cache-Control", "public, max-age=31536000"))
	l.Use(backend)
	h.Use(l)

	h.Use(backend)

	return h
}
```

## WAF with CEL rules

The [`waf`](pkg/waf) package runs [CEL](https://github.com/google/cel-go) expressions against incoming requests. Rules compile inside `SetRules`, so the hot path never parses or type-checks, and rules can be swapped atomically at runtime.

```go
import (
	"net/http"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/waf"
)

w := waf.New()
_ = w.SetRules([]waf.Rule{{
	ID:         "block-sqli",
	Expression: `request.query.contains("' OR '1'='1") || request.path.matches("(?i).*union.*select.*")`,
	Action:     waf.ActionBlock,
	Status:     http.StatusForbidden,
}})

s := parapet.NewFrontend()
s.Use(w)
```

See [`pkg/waf/doc.go`](pkg/waf/doc.go) for the full list of `request.*` fields and helper functions exposed to expressions.

## JWT authentication

The [`authn`](pkg/authn) package verifies `Authorization: Bearer` tokens with
`authn.JWT`. The accepted signature algorithms are **pinned by the caller** — a
token signed with any other algorithm (including `none`) is rejected, which
prevents algorithm-confusion attacks. The signature, `exp`/`nbf` (with leeway),
and optional `iss`/`aud` claims are all verified; verified claims are placed on
the request context for downstream handlers.

Algorithms are pinned with this package's own constants (`authn.HS256`,
`authn.RS256`, …), so callers don't import a JOSE library — only `pkg/authn`.

```go
import "github.com/moonrhythm/parapet/pkg/authn"

m := authn.JWT([]byte(secret), authn.HS256) // []byte for HMAC; a public key for RS*/ES*/EdDSA
m.Issuer = "https://issuer.example.com"
m.Audience = "my-api"
s.Use(m)

// downstream
claims, ok := authn.JWTClaimsFromContext(r.Context())
```

### Rotating keys from a remote JWKS

For tokens signed by an OIDC provider (Auth0, Okta, Google, …), verify against
the provider's `jwks_uri` instead of a static key with `authn.JWKS`. It fetches
the key set over HTTP and caches it, picking up signing-key **rotation** without
a restart: a stale cache is refreshed in the background while the last good set
keeps serving, and a token bearing an unknown `kid` triggers a single-flighted
refetch (rate-limited so bogus `kid`s can't hammer the endpoint). Refresh
failures are **fail-static** — once a set has been fetched, a later fetch error
never starts rejecting valid tokens. The algorithm allowlist is still mandatory
and enforced exactly as above.

```go
m := authn.JWTFromKeySource(
	&authn.JWKS{URL: "https://issuer.example.com/.well-known/jwks.json"},
	authn.RS256, // pin the accepted algorithm(s)
)
m.Issuer = "https://issuer.example.com"
m.Audience = "my-api"
s.Use(m)
```

`JWKS` exposes `RefreshInterval` (cache TTL, default 15m), `MinRefreshInterval`
(unknown-`kid` refetch rate limit, default 1m), `Client`, and `MaxResponseBytes`.

## Response caching

The [`cache`](pkg/cache) package is a CDN-style, honor-origin response cache. It caches a response **only** when the origin opts in with explicit freshness (`Cache-Control: s-maxage`/`max-age` or `Expires`); refuses `private`/`no-store`/`no-cache`, `Set-Cookie`, and `Vary: *`; honors `Vary`; serves `GET`/`HEAD` only; and ignores the client's request `Cache-Control` so a client can't bust the shared cache. Concurrent misses for one key collapse into a single origin fetch (single-flight), and it's fail-static — any storage error degrades to a miss, never an error to the client. Every response is tagged `X-Cache: HIT|MISS`.

Two storage backends ship: an in-memory one (lost on restart) and a disk-backed one (survives restarts, streams bodies to disk). Both bound their total size with LRU eviction plus a per-object cap. Mount it ahead of the upstream/handler whose responses it should cache.

```go
import "github.com/moonrhythm/parapet/pkg/cache"

// Disk-backed, 1 GiB on disk, 8 MiB per object; or cache.NewMemory(size) for RAM.
store, err := cache.NewDisk("/var/cache/app", 1<<30)
if err != nil {
	log.Fatal(err)
}

h := host.New("static.example.com")
h.Use(cache.New(store, cache.Options{MaxFileSize: 8 << 20}))
h.Use(upstream.SingleHost("origin.default.svc.cluster.local", &upstream.HTTPTransport{}))
s.Use(h)
```

`Options` also exposes `Cacheable` (a per-request predicate to exclude vetted paths), `InvalidatedAfter` (out-of-band purge), `LockTimeout`, and `DecoupleFill` (keep a slow client from stalling waiting followers). Because only origin-opted-in public content is cached, mark per-user or authorization-sensitive responses uncacheable at the origin.

### Forcing caching for an origin you don't control

`Options.Override` is a hook that returns a forced caching policy, overriding the origin's `Cache-Control` — so you can cache an origin that sends no (or unwanted) cache headers. It is called on each GET/HEAD fill with the **request and the origin's response** (status + headers), so the decision can key on anything in the request (host, path, extension) *and* the response (`Content-Type`, `Content-Length`, status). Return `nil` to honor the origin. The forced policy is baked into the **stored entry only**, so the served `Cache-Control` stays the origin's and doesn't propagate downstream.

```go
cache.New(store, cache.Options{
    Override: func(r *http.Request, status int, header http.Header) *cache.Override {
        switch {
        case status != http.StatusOK:
            return nil                                       // only force 200s
        case strings.HasPrefix(header.Get("Content-Type"), "image/"):
            return &cache.Override{TTL: time.Hour}           // force images for 1h
        case r.Host == "static.example.com" && strings.HasSuffix(r.URL.Path, ".js"):
            return &cache.Override{TTL: 24 * time.Hour}      // force this host's JS for a day
        default:
            return nil                                        // everything else: respect upstream
        }
    },
})
```

`status` and `header` are the live origin response — read them, don't mutate them.

`Override.Mode` chooses how far the force reaches over the origin's own directives — the safety trade-off is yours per request:

| Mode | Overrides | Still refuses |
|---|---|---|
| `OverrideBalanced` (default) | missing freshness, `no-cache`, `max-age`, `Expires` | `no-store`, `private`, `Set-Cookie`, `Vary: *`, non-cacheable status, oversize, `Authorization` without a shared opt-in |
| `OverrideConservative` | only *missing* freshness | everything the origin says (`no-cache`/`no-store`/`private`/`max-age` all honored) |
| `OverrideAggressive` | almost everything, incl. `no-store`/`private`/`Authorization` | `Set-Cookie`, `Vary: *`, non-cacheable status, oversize |

> ⚠️ Forcing trusts you to target cacheable paths. The cache key ignores the request's `Cookie` and `Authorization`, so **don't force per-user paths**: even `OverrideBalanced` will cross-user-leak a response gated by a session `Cookie` when the origin sends no `Set-Cookie`/`private`/`no-store`. `OverrideAggressive` additionally bypasses the `Authorization` gate. Scope the hook to known-public paths (or use `Options.Cacheable`).

`Override.StaleWhileRevalidate` / `StaleIfError` force the RFC 5861 windows too (see below). For an unconditional default instead of a per-request hook, use `Options.DefaultStaleWhileRevalidate` / `DefaultStaleIfError`.

### Stale serving (RFC 5861)

When the origin sets `Cache-Control: stale-while-revalidate=<s>` or `stale-if-error=<s>` on a cacheable response, the cache may serve the entry **after** it goes stale:

- **`stale-while-revalidate`** — within the window, a stale entry is served immediately (`X-Cache: STALE`) while a single background revalidation refreshes it, so the client never waits on the origin. The detached fetch is bounded by `Options.RevalidateTimeout` (default 30s).
- **`stale-if-error`** — past any `stale-while-revalidate` window, the cache contacts the origin and, only if it answers with a server error (5xx), serves the stale entry instead of the error (`X-Cache: STALE`).

`must-revalidate`/`proxy-revalidate` suppress both. The client's request `Cache-Control` is ignored (only the origin's response directives are honored), consistent with the rest of the cache. Note that an entry offering these windows is retained in storage until it is past the larger window (not just past freshness), so stale-if-error still has something to fall back to — total size remains bounded by the backend's LRU cap.

**Forcing stale serving for an origin you don't control.** Set `Options.DefaultStaleWhileRevalidate` / `Options.DefaultStaleIfError` to apply a window to any cacheable response that doesn't carry the directive itself. An explicit directive on the response still wins, and `must-revalidate`/`proxy-revalidate` still suppress it. These stay **private to this cache** — the served `Cache-Control` remains the origin's, so the policy doesn't propagate to downstream clients or caches.

```go
cache.New(store, cache.Options{
    DefaultStaleWhileRevalidate: 30 * time.Second,
    DefaultStaleIfError:         24 * time.Hour,
})
```

Alternatively, inject the directive with a `headers` middleware mounted **below** the cache (so the cache sees it on the response). The cache parses every `Cache-Control` header, so this adds the windows without clobbering the origin's `max-age` — but unlike the options above, the injected directive **is** served to clients:

```go
h.Use(cache.New(store, cache.Options{}))                                       // outer
h.Use(headers.AddResponse("Cache-Control", "stale-while-revalidate=30"))       // below the cache
h.Use(upstream.SingleHost("origin...", &upstream.HTTPTransport{}))             // inner
```

### Purging

[`cache/purge`](pkg/cache/purge) invalidates cached entries by **host, URL, path prefix, or surrogate tag** (the origin's `Cache-Tag`). A `purge.Table` plugs into `Options.InvalidatedAfter`; invalidation is lazy (issuing a purge is O(1), a purged entry is reclaimed on its next lookup) and immediate (a purged entry is never served). Memory is bounded — an overflowing scope map folds into a global flush — and epochs are monotonic, so an NTP step-back can't un-purge.

```go
pt := purge.New()
c := cache.New(store, cache.Options{InvalidatedAfter: pt.InvalidatedAfter})

pt.PurgeURL("example.com", "/a")        // one URL: all methods/schemes/Vary variants
pt.PurgePrefix("example.com", "/blog")  // a section, boundary-aware (/blog, not /blogger)
pt.PurgeTag("product-42")               // every response carrying this surrogate key, any host
pt.FlushAll()                           // everything

go func() { for range time.Tick(5 * time.Minute) { pt.Reap(store) } }() // proactively reclaim bytes
```

`Snapshot`/`Restore` serialize the table so purges survive a restart (persist however you like). It's the engine [parapet-ingress-controller](https://github.com/moonrhythm/parapet-ingress-controller) builds its control-plane purge distribution on top of.

## Weighted and least-connection load balancing

`upstream.NewRoundRobinLoadBalancer` weights every target equally. Two strategies
bias by a per-`Target` `Weight` (values `<= 0` count as 1), each optimizing a
different axis:

- `upstream.NewWeightedRoundRobinLoadBalancer` distributes request **count** in
  proportion to weight, using smooth weighted round-robin (the nginx algorithm),
  so a heavy target's picks are interleaved rather than dealt in a burst. With
  equal weights it is plain round-robin.
- `upstream.NewLeastConnLoadBalancer` routes each request to the target with the
  fewest in-flight requests (weighted: lowest `active/Weight`), so it tracks
  **concurrency** rather than count — which adapts to slow backends and
  long-lived requests a count-based balancer misses. A request stays counted
  until its response body is closed, which parapet's reverse proxy always does.
  Set `Target.MaxConcurrent` to cap a target's in-flight requests (the
  **bulkhead** pattern): the cap is hard and never exceeded, surplus requests
  route to an under-cap target, and when every target is full the balancer sheds
  with `503` rather than overloading a saturated origin. A slot is freed only when
  the response body is closed, so bound **total** request time (a request-context
  deadline the transport honors) to keep a backend that stalls mid-body from
  latching the cap — a response-header or idle timeout alone does not cover a
  mid-body stall.

```go
s.Use(upstream.New(upstream.NewWeightedRoundRobinLoadBalancer([]*upstream.Target{
	{Host: "10.0.0.1:8080", Transport: &upstream.HTTPTransport{}, Weight: 3},
	{Host: "10.0.0.2:8080", Transport: &upstream.HTTPTransport{}, Weight: 1},
})))
```

## Load balancing with passive health checks

`upstream.NewRoundRobinLoadBalancer` spreads requests evenly but keeps routing to
a dead backend. `upstream.NewEjectingLoadBalancer` adds passive health checking
(outlier ejection): after a target returns `MaxFails` consecutive failures it is
ejected from rotation for `EjectTimeout` (doubling on each repeat ejection, up to
`MaxEjectTimeout`), then allowed back with no background probing. A single
success clears its failure count. If every target is ejected the balancer fails
open and keeps routing, so a transient outage cannot black-hole all traffic.

```go
lb := upstream.NewEjectingLoadBalancer([]*upstream.Target{
	{Host: "10.0.0.1:8080", Transport: &upstream.HTTPTransport{}},
	{Host: "10.0.0.2:8080", Transport: &upstream.HTTPTransport{}},
})
lb.MaxFails = 3                      // consecutive failures before ejection
lb.EjectTimeout = 30 * time.Second   // base cooldown
s.Use(upstream.New(lb))
```

By default only transport errors (other than a client-canceled request) count as
failures. Set `lb.IsFailure` to also treat responses such as 5xx as failures:

```go
lb.IsFailure = func(resp *http.Response, err error) bool {
	return err != nil || (resp != nil && resp.StatusCode >= 500)
}
```

Pair it with `prom.Upstream()` (wired into `Upstream.OnRoundTrip`) to watch
ejections take effect: traffic shifts off a failing backend in
`parapet_upstream_requests{host,status}`. Wire each reliability balancer's
`OnStateChange` to `prom.UpstreamState()` to make the state machine itself
observable — `parapet_upstream_state_transitions_total{host,from,to,reason}`
(trips, ejections, recoveries, half-open probes) plus a current-state gauge — and
`prom.Upstream()` also counts fail-fast 503s in `parapet_upstream_fast_rejects_total`.

`upstream.NewCircuitBreakingLoadBalancer` goes a step further: it **fails fast**.
An open target is rejected *without a round-trip* (so a request never pays the
dead backend's connect+timeout), and when every target is open it returns 503
rather than failing open — shedding load instead of hammering a dead origin.
After `FailureThreshold` consecutive failures a target opens for `OpenTimeout`
(doubling per repeat trip, up to `MaxOpenTimeout`), then admits a small half-open
trickle (`HalfOpenMaxProbes`) to test recovery: `SuccessThreshold` successes close
it, one failure re-opens it.

```go
lb := upstream.NewCircuitBreakingLoadBalancer([]*upstream.Target{
	{Host: "10.0.0.1:8080", Transport: &upstream.HTTPTransport{}},
	{Host: "10.0.0.2:8080", Transport: &upstream.HTTPTransport{}},
})
lb.FailureThreshold = 5
lb.OpenTimeout = 5 * time.Second
s.Use(upstream.New(lb))
```

Use `EjectingLoadBalancer` when you want fail-*open* (keep routing during a total
outage); use `CircuitBreakingLoadBalancer` when you want fail-*fast* (shed load).
The same `IsFailure` hook applies. Both ignore `Target.Weight`.

`upstream.NewLatencyEjectingLoadBalancer` catches what those two miss — a **gray
failure**, a backend still returning 200s but far slower than its peers. A target
whose decayed mean time-to-first-byte exceeds `EjectionFactor` × the **pool median**
is ejected and re-probed. Because the test is relative to the pool, it self-tunes: a
uniform slowdown raises every target and the median together, so no one is an
outlier (guard rails — a max-ejection cap and a panic threshold — keep a systemic
slowdown from draining the pool). It is latency-only: pair it with the circuit
breaker or `EjectingLoadBalancer` for error ejection.

```go
lb := upstream.NewLatencyEjectingLoadBalancer([]*upstream.Target{
	{Host: "10.0.0.1:8080", Transport: &upstream.HTTPTransport{}},
	{Host: "10.0.0.2:8080", Transport: &upstream.HTTPTransport{}},
	{Host: "10.0.0.3:8080", Transport: &upstream.HTTPTransport{}},
})
lb.EjectionFactor = 3 // eject a target 3× slower than the pool median
s.Use(upstream.New(lb))
```

## Hedging (speculative retry)

`upstream.NewHedgingLoadBalancer` wraps any balancer to cut **tail latency**: if an
idempotent, body-less request hasn't responded within `HedgeDelay`, it sends a
duplicate to another target (the wrapped balancer self-selects a different one),
returns whichever response arrives first, and cancels the loser. The race happens
inside the `RoundTripper`, so the proxy only ever sees the winner.

```go
h := upstream.NewHedgingLoadBalancer(lb) // lb is any balancer
h.HedgeDelay = 30 * time.Millisecond     // ~p95; <= 0 disables (zero-cost pass-through)
s.Use(upstream.New(h))
```

`MaxHedge` (default 1) caps the fan-out. Non-idempotent requests, and a request
already inside the retry loop, pass straight through. Because losing legs are
cancelled with `context.Canceled`, a custom `IsFailure` on the wrapped balancer
must exclude it (the default does), or hedging would slowly eject the healthy
backend it raced.

## Active health checks

The balancers above are **passive** — they learn a target is unhealthy only from
real traffic's failures. `upstream.NewActiveHealthCheck` adds **active** probing:
it wraps any balancer and probes each target out-of-band (one background goroutine
per target), routing only to those answering. Pass the **same** `[]*Target` to both
the balancer and the wrapper so their indices line up:

```go
targets := []*upstream.Target{
	{Host: "10.0.0.1:8080", Transport: tr},
	{Host: "10.0.0.2:8080", Transport: tr},
}
ahc := upstream.NewActiveHealthCheck(targets, upstream.NewRoundRobinLoadBalancer(targets))
ahc.Path = "/healthz"
ahc.Interval = 5 * time.Second
ahc.UnhealthyThld = 3 // down after 3 consecutive failed probes; HealthyThld re-admits
s.Use(upstream.New(ahc))
```

Active and passive **compose**: the health gate only *removes* candidates, and the
wrapped balancer keeps its own strategy over the survivors — a weighted balancer
keeps its exact ratio, the circuit breaker still trips, least-conn still balances.
A target must pass **both** to be picked. When the gate marks **every** target down,
each balancer falls back to its own all-down policy: round-robin / ejecting /
latency-ejecting / least-conn route best-effort (so a broken probe path can't 503 a
whole healthy pool), while the circuit breaker still sheds. (Least-conn still sheds
on its *capacity* cap — `MaxConcurrent` — independently of health.)

Probing auto-starts on the first request and, when served by a `parapet.Server`,
stops on graceful shutdown. For a bare `RoundTripper`, or to bound the prober's
lifetime explicitly, call `ahc.Start(ctx)` before serving and `ahc.Close()` after.
By default a slot probes through each `Target.Transport` (exercising the real pool);
set `ProbeTransport` to isolate probe traffic. A held probe is bounded by `Timeout`,
and targets begin **up** by default (`StartUnhealthy` flips to fail-closed) so a
misconfigured probe path cannot black-hole a fresh deploy. The probe uses `http`;
for a target on the dynamic multi-scheme `Transport` set `ahc.Scheme` to `"h2c"` or
`"unix"` (the dedicated transports force their own scheme and ignore it).

## Traffic mirroring (shadowing)

`mirror.New` tees a copy of matched/sampled **requests** to a separate destination
(a "canary") so you can exercise a new build with real production traffic. It is
**fire-and-forget**: the primary request and its response are never affected — a
mirror that is slow, queue-full, or panicking is dropped or recovered, never
propagated. The canary's response is discarded.

```go
mr := mirror.New()
mr.Match = func(r *http.Request) bool { return r.Method == http.MethodGet }
mr.SampleRate = 0.1                  // shadow 10% of matched requests
mr.Observe = prom.Mirror()           // optional outcome/latency metrics
mr.Use(upstream.SingleHost("canary:8080", &upstream.HTTPTransport{}))

s.Use(mr)                            // tees, then falls through to the real chain
s.Use(upstream.SingleHost("prod:8080", &upstream.HTTPTransport{}))
```

A fixed worker pool (`Workers`, default 8) bounds mirror concurrency; a full
`QueueSize` queue drops rather than blocking. The request body is buffered up front
(bounded by `MaxBodyBytes`) so the primary and the mirror read byte-identical bytes;
an over-cap body skips the mirror. Each mirror runs on a detached
`context.Background()` deadline (`Timeout`), so a client disconnect never cancels it.
The mirrored request is marked (`X-Mirror: 1` by default; `DisableMark` to go fully
transparent) so the canary can no-op side effects. End-to-end credentials are
replayed by design — use `Match` to exclude sensitive routes.

## Trusted proxies

Parapet only reads `X-Forwarded-*` and `X-Real-IP` when the connection comes from a trusted CIDR. Configure trust with `TrustCIDRs(...)` or accept the defaults from `Trusted()` (standard private and loopback ranges). Servers created with `NewFrontend()` start with no trusted proxies by default.

## PROXY protocol

An L4 load balancer (AWS NLB, HAProxy in TCP mode, …) terminates the TCP
connection, so without help the proxy sees the balancer's IP, not the client's —
and an L4 balancer adds no `X-Forwarded-For`. The [`proxyprotocol`](pkg/proxyprotocol)
package reads the HAProxy **PROXY protocol** header (v1 and v2) that such
balancers prepend and rewrites the connection's `RemoteAddr` to the real client,
so `ratelimit`, `waf`, `logger`, and the trust logic above all see the right
address. Mount it with `Server.ModifyConnection`; the header is parsed lazily on
the connection's first read, off the accept loop.

```go
// Only the listed CIDRs (your load balancer) may set a client address; a direct
// connection from outside them is passed through untouched and cannot spoof one.
pp := proxyprotocol.New("10.0.0.0/8")

s := parapet.NewFrontend()
s.ModifyConnection(pp.ModifyConnection)
```

Set `Require` to reject a trusted connection that arrives without a PROXY header
(use it when every connection from the balancer is guaranteed to carry one); by
default such a connection is served with its real peer address.

## Performance tuning

`Server.ShareProtoSlice` makes that server's proxy write a single shared
`[]string` for `X-Forwarded-Proto` instead of allocating a fresh slice per
request, saving one allocation on every request that sets the header (~16% on
the distrust path in the proxy benchmarks). It is off by default and scoped to
the server.

This is **unsafe if any middleware mutates the `X-Forwarded-Proto` value slice in
place** — e.g. `headers.MapRequest("X-Forwarded-Proto", …)` or code doing
`r.Header["X-Forwarded-Proto"][0] = …` — because the mutation would corrupt the
shared slice for every subsequent request. Appending (`headers.AddRequest`) is
safe. Enable it only if you control the whole middleware chain, and set it
before serving:

```go
s := parapet.NewBackend()
s.ShareProtoSlice = true
```

The `hsts` and `authn` middlewares expose the same opt-in for their fixed
response headers via a `ShareValueSlice` field (off by default), sharing one
`Strict-Transport-Security` / `WWW-Authenticate` slice across requests:

```go
hsts.HSTS{MaxAge: 365 * 24 * time.Hour, ShareValueSlice: true}
```

The same caveat applies — only enable it when nothing in the chain mutates that
response header's value slice in place.

## License

[MIT](LICENSE)
