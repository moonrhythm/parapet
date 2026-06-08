# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make          # vet + lint + test (default)
make test     # go test -race ./...
make vet      # go vet ./...
make lint     # golangci-lint run

# Run a single package's tests
go test -race ./pkg/upstream/...

# Run a single test
go test -race -run TestFoo ./pkg/upstream/...
```

Linting uses `golangci-lint` with `errcheck` disabled and `fieldalignment` enabled. The `.golangci.yaml` excludes vet rules from `_test.go` files.

## Architecture

Parapet is a composable reverse proxy framework for Go. It is not a binary — it is a library that callers import and configure programmatically.

### Core abstractions (root package)

- **`Middleware` interface** — `ServeHandler(http.Handler) http.Handler`. Every feature is a `Middleware`.
- **`Middlewares`** — an ordered slice of `Middleware` values, applied in reverse order (outermost first, like an onion).
- **`Server`** — wraps `http.Server` with a `Middlewares` chain and adds TLS, H2C, graceful shutdown (30 s grace, 10 s wait), and reuseport support. Three constructors: `New()` for general use, `NewFrontend()` for edge-facing, `NewBackend()` for internal services.
- **`Use(m Middleware)`** — appends to the server's middleware chain.

### `pkg/` packages

Each subdirectory under `pkg/` is a self-contained middleware or feature:

| Package | What it does |
|---|---|
| `upstream` | Reverse proxy and load balancing (RoundRobin). Supports HTTP, H2C, HTTPS transports. |
| `host` | Virtual-host routing — matches `Host` header with optional wildcard prefixes. |
| `location` | Path routing — exact, prefix, and regex matchers. |
| `block` | Conditional middleware container: match a request, then apply its own inner chain. |
| `ratelimit` | Fixed-window (per-second/minute/hour), concurrent, and leaky-bucket limiters. |
| `compress` | Content-negotiated compression: Gzip, Brotli, Deflate. |
| `headers` | Request/response header manipulation (set, delete, copy). |
| `redirect` | HTTPS redirect, www/non-www normalization, custom redirects. |
| `requestid` | Injects/propagates a request ID header. |
| `logger` | Structured request logging. |
| `healthz` | Health-check endpoint. |
| `hsts` | Sets Strict-Transport-Security (with preload support). |
| `timeout` | Per-request deadline enforcement. |
| `cors` | CORS header handling. |
| `prom` | Prometheus metrics (requests, connections, network bytes). |
| `body` | Request body limiting and buffering. |
| `stripprefix` | Strips a URL path prefix before proxying. |
| `router` | URL routing. |
| `fileserver` | Static file serving. |
| `authn` | Authentication helpers (JWT, basic auth). |
| `proxyprotocol` | HAProxy PROXY protocol (v1/v2) reader; rewrites conn `RemoteAddr` to the real client behind an L4 LB. Wired via `Server.ModifyConnection`. |
| `gcp` / `stackdriver` / `trace` | Google Cloud integration and distributed tracing (OpenCensus/OpenTelemetry). |

### Middleware composition pattern

Middlewares wrap each other; the last `Use()` call is the innermost handler. The `block` package enables conditional branching: a `Block` tests each request against matcher(s), and if matched, routes it through its own inner `Middlewares` instead of falling through.

The proxy header logic lives in `proxy.go`: it reads `X-Forwarded-*` and `X-Real-IP` only from trusted upstreams, configured via `TrustCIDRs()` or `Trusted()`.
