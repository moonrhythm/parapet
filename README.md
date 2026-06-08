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
| [`upstream`](pkg/upstream) | Reverse proxy and load balancing (round-robin) over HTTP, H2C, or HTTPS |
| [`host`](pkg/host) | Virtual-host routing on the `Host` header, with wildcard prefixes |
| [`location`](pkg/location) | Path routing — exact, prefix, and regexp matchers |
| [`router`](pkg/router) | Simple URL router |
| [`block`](pkg/block) | Conditional middleware container — match a request, then apply an inner chain |
| [`ratelimit`](pkg/ratelimit) | Fixed-window, concurrent, and leaky-bucket limiters |
| [`compress`](pkg/compress) | Content-negotiated compression (Gzip, Brotli, Deflate) |
| [`cache`](pkg/cache) | HTTP response cache — honor-origin policy, in-memory or disk backend, single-flight fills, `X-Cache` tag |
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
