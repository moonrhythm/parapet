package timeout

import (
	"context"
	"net/http"
	"time"
)

// NewRequestDeadline creates a [RequestDeadline] middleware that bounds the
// TOTAL request time to d.
func NewRequestDeadline(d time.Duration) *RequestDeadline {
	return &RequestDeadline{Timeout: d}
}

// RequestDeadline arms a deadline over the ENTIRE request — response headers AND
// the body stream — by deriving a [context.WithTimeout] from the request context
// and serving the downstream handler with it. When the deadline elapses the
// request context is cancelled; a transport that honors the request context
// (parapet's upstream transports do) then aborts the in-flight call, including a
// backend that has already written headers but stalls mid-body.
//
// # How it differs from Timeout
//
//   - [Timeout] is a write-header deadline: it fires only until the upstream
//     writes response headers, then stops mattering (its timeoutRW is irrelevant
//     once headers are written). A backend that sends headers and then stalls
//     mid-body is NOT bounded by Timeout. On expiry Timeout writes its own 504
//     Gateway Timeout response.
//   - RequestDeadline is a TOTAL request deadline (headers + body). It is a bare
//     context wrapper: no SSE detection, no streaming heuristics, no response
//     buffering. It does NOT write a custom timeout response of its own — the
//     context deadline simply propagates and the downstream handler / transport
//     surfaces the resulting error (typically a 502/504 from pkg/upstream once
//     the context-cancelled call fails).
//
// # Motivation
//
// pkg/upstream's LeastConnLoadBalancer MaxConcurrent bulkhead holds a slot until
// the response body is closed. A backend that writes headers then stalls
// mid-body keeps its slot indefinitely — no http.Transport timeout covers that
// stall (ResponseHeaderTimeout bounds only time-to-headers; IdleConnTimeout reaps
// only idle pooled connections). After MaxConcurrent such stalls the target sheds
// all traffic permanently: the cap becomes a latch, not a limiter. Capping TOTAL
// request time via a request-scoped context deadline the transport honors is the
// in-tree mitigation, and that is exactly what RequestDeadline provides — which
// Timeout cannot, since it disarms once upstream headers are written.
//
// # WARNING: do not blanket-deploy
//
// A total request deadline will KILL legitimate long-lived responses — Server-Sent
// Events, streaming responses, WebSocket-style upgrades, and large file downloads
// — because those intentionally keep the body open far longer than any sane
// header-to-completion bound. Do NOT apply RequestDeadline globally. Apply it
// PER-ROUTE (via pkg/location or pkg/block) only to endpoints whose total time
// genuinely should be bounded, and exclude streaming/download routes.
//
// A Timeout (this field) of <= 0 makes ServeHandler a pass-through no-op,
// matching [Timeout].
type RequestDeadline struct {
	Timeout time.Duration
}

// ServeHandler implements middleware interface
func (m RequestDeadline) ServeHandler(h http.Handler) http.Handler {
	if m.Timeout <= 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), m.Timeout)
		defer cancel()

		h.ServeHTTP(w, r.WithContext(ctx))
	})
}
