package upstream

import (
	"net/http"
	"time"
)

// RoundTripInfo reports one upstream round-trip — a single attempt to a backend,
// so a retried request produces one per attempt — to a RoundTripFunc.
//
//nolint:govet // fields ordered for readability, not pointer-packing
type RoundTripInfo struct {
	// Host is the resolved upstream target the request was sent to (r.URL.Host after
	// load balancing). Unlike the client Host header, it is operator-configured and
	// therefore bounded, so it is safe as a metric label. It may be empty when the
	// load balancer rejected the request before choosing a target (ErrUnavailable).
	Host string

	// Duration is the time to the response headers: the transport round-trip
	// (connect + send request + time-to-first-byte), measured before the body is
	// streamed on to the client. It is set on both success and error.
	Duration time.Duration

	// Status is the upstream response's status code, or 0 when the round-trip failed
	// before any response (Err is then non-nil).
	Status int

	// Err is the transport error — connection refused, timeout, ErrUnavailable when
	// the load balancer had no target, etc. — or nil once a response was received,
	// regardless of that response's status code.
	Err error
}

// RoundTripFunc observes an upstream round-trip. Assign one to Upstream.OnRoundTrip
// to make the origin observable — see prom.Upstream for Prometheus metrics. It is
// invoked once per attempt (including each retry), synchronously on the request
// goroutine, right after the transport returns and before the response unwinds back
// through the proxy. The callee owns its own concurrency.
type RoundTripFunc func(r *http.Request, info RoundTripInfo)
