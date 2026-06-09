package healthz_test

import (
	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/healthz"
)

// Expose the default /healthz endpoint. By default it answers liveness on
// /healthz and readiness on /healthz?ready=1; readiness flips to 503 once the
// server begins graceful shutdown, so a load balancer drains the instance.
func ExampleNew() {
	s := parapet.New()
	s.Use(healthz.New())
	// liveness:  GET /healthz
	// readiness: GET /healthz?ready=1
}

// Serve the health endpoint on a custom path and accept requests that carry a
// real Host header (by default only requests addressed to an IP are answered,
// which suits an in-cluster kubelet probe).
func ExampleHealthz() {
	m := healthz.New()
	m.Path = "/_status"
	m.Host = true

	s := parapet.New()
	s.Use(m)
}

// Drive the readiness and liveness flags from application code: report not-ready
// while warming up, then mark unhealthy if a dependency check fails.
func ExampleHealthz_Set() {
	m := healthz.New()

	m.SetReady(false) // fail readiness until startup finishes
	// ... load config, warm caches, connect to dependencies ...
	m.SetReady(true) // start accepting traffic

	m.Set(false) // a background check failed: fail liveness so we get restarted

	s := parapet.New()
	s.Use(m)
}
