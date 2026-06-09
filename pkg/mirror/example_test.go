package mirror_test

import (
	"net/http"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/mirror"
	"github.com/moonrhythm/parapet/pkg/prom"
	"github.com/moonrhythm/parapet/pkg/upstream"
)

// Shadow a sample of production GET traffic to a canary backend, fire-and-forget: the
// mirror never affects the primary request or its response. Mark and sample so the
// canary can detect (and no-op side effects of) shadow traffic and so only a fraction
// is teed; Observe wires outcome/latency metrics.
func ExampleNew() {
	mr := mirror.New()
	mr.Match = func(r *http.Request) bool { return r.Method == http.MethodGet }
	mr.SampleRate = 0.1        // shadow 10% of matched requests
	mr.Observe = prom.Mirror() // optional outcome/latency metrics
	mr.Use(upstream.SingleHost("canary:8080", &upstream.HTTPTransport{}))

	s := parapet.NewFrontend()
	s.Use(mr)                                                          // tees, then falls through to the real chain
	s.Use(upstream.SingleHost("prod:8080", &upstream.HTTPTransport{})) // the production backend
}
