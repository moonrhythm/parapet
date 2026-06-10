package timeout_test

import (
	"net/http"
	"time"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/location"
	"github.com/moonrhythm/parapet/pkg/timeout"
)

// Bound how long the downstream handler may take to start responding: if it
// hasn't written a header within the deadline, the request's context is
// cancelled and a 504 Gateway Timeout is sent.
func ExampleNew() {
	s := parapet.New()
	s.Use(timeout.New(30 * time.Second))
	// s.Use(upstream.SingleHost(...)) — the handler the deadline applies to.
}

// Replace the default 504 response with a custom one by setting TimeoutHandler.
func ExampleTimout() {
	m := timeout.New(5 * time.Second)
	m.TimeoutHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		http.Error(w, "upstream is taking too long, try again shortly", http.StatusGatewayTimeout)
	})

	s := parapet.New()
	s.Use(m)
}

// timeout.Timeout is a non-breaking alias for the (typo'd) Timout struct, so new
// code can spell the type correctly while existing Timout users keep working.
func ExampleTimeout() {
	var m timeout.Timeout // same type as timeout.Timout
	m.Timeout = 5 * time.Second

	s := parapet.New()
	s.Use(m)
}

// RequestDeadline bounds the TOTAL request time (headers AND body) via the
// request context, so it also aborts a backend that writes headers then stalls
// mid-body — unlike timeout.New/Timout, which disarms once headers are written.
//
// Apply it PER-ROUTE, never globally: a blanket total deadline would kill
// legitimate long-lived responses (SSE, streaming, large downloads). Here only
// the /api prefix is bounded; streaming routes elsewhere are left untouched.
func ExampleRequestDeadline() {
	api := location.Prefix("/api")
	api.Use(timeout.NewRequestDeadline(30 * time.Second))
	// api.Use(upstream.SingleHost(...)) — the bounded handler.

	s := parapet.New()
	s.Use(api)
}
