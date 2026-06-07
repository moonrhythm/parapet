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

```go
import (
	jose "github.com/go-jose/go-jose/v4"
	"github.com/moonrhythm/parapet/pkg/authn"
)

m := authn.JWT([]byte(secret), jose.HS256) // []byte for HMAC; a public key for RS*/ES*/EdDSA
m.Issuer = "https://issuer.example.com"
m.Audience = "my-api"
s.Use(m)

// downstream
claims, ok := authn.JWTClaimsFromContext(r.Context())
```

## Trusted proxies

Parapet only reads `X-Forwarded-*` and `X-Real-IP` when the connection comes from a trusted CIDR. Configure trust with `TrustCIDRs(...)` or accept the defaults from `Trusted()` (standard private and loopback ranges). Servers created with `NewFrontend()` start with no trusted proxies by default.

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
