package requestid_test

import (
	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/requestid"
)

// Assign every request a unique ID. New trusts an incoming X-Request-Id from an
// upstream proxy (reusing it for distributed tracing) and otherwise generates a
// fresh UUIDv4. The ID is written to both the request and response headers and
// recorded as "requestId" in the structured access log.
func ExampleNew() {
	s := parapet.New()
	s.Use(requestid.New())
	// s.Use(upstream.SingleHost(...)) — the ID is forwarded to the upstream.
}

// At the edge, do not trust a client-supplied request ID: always mint a new one
// so callers cannot spoof or poison the value used in logs and upstream headers.
// A custom header key is used here instead of the default X-Request-Id.
func ExampleRequestID() {
	s := parapet.New()
	s.Use(&requestid.RequestID{
		TrustProxy: false,
		Header:     "X-Trace-Id",
	})
}
