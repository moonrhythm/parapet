package trace_test

import (
	"net/http"
	"strings"

	"go.opencensus.io/plugin/ochttp/propagation/b3"
	octrace "go.opencensus.io/trace"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/trace"
)

// Add OpenCensus server-side tracing to the proxy. Each request becomes a span;
// an exporter registered elsewhere (Stackdriver, Zipkin, ...) ships them.
func ExampleNew() {
	s := parapet.New()
	s.Use(trace.New())
	// s.Use(upstream.SingleHost("10.0.0.1:8080")) — the proxied handler runs inside the span.
}

// Sample only a fraction of requests so a high-traffic edge does not export a
// span for every request, and label spans with the path instead of the full URL.
func ExampleTrace() {
	m := trace.New()
	m.StartOptions.Sampler = octrace.ProbabilitySampler(0.05) // ~5% of traces
	m.FormatSpanName = func(r *http.Request) string {
		return r.Method + " " + r.URL.Path
	}

	s := parapet.New()
	s.Use(m)
}

// Read incoming trace context using the B3 headers (X-B3-TraceId, ...) emitted
// by Zipkin-style clients instead of the default format, so spans join the
// caller's trace. Mark this hop as a public endpoint so an untrusted client's
// span context starts a fresh, linked trace rather than being trusted as parent.
func ExampleTrace_propagation() {
	m := trace.New()
	m.Propagation = &b3.HTTPFormat{}
	m.IsPublicEndpoint = true

	s := parapet.New()
	s.Use(m)
}

// Decide sampling per request: always sample internal/debug traffic, fall back
// to a low probability for everything else.
func ExampleTrace_getStartOptions() {
	always := octrace.StartOptions{Sampler: octrace.AlwaysSample()}
	sampled := octrace.StartOptions{Sampler: octrace.ProbabilitySampler(0.01)}

	m := trace.New()
	m.GetStartOptions = func(r *http.Request) octrace.StartOptions {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			return always
		}
		return sampled
	}

	s := parapet.New()
	s.Use(m)
}
