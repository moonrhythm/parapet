package timeout_test

import (
	"net/http"
	"time"

	"github.com/moonrhythm/parapet"
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
