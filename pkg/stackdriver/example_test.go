package stackdriver_test

import (
	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/stackdriver"
	"github.com/moonrhythm/parapet/pkg/trace"
)

// Register the OpenCensus Stackdriver (Google Cloud Trace) exporter once at
// startup, then mount the tracing middleware so every request is reported as a
// span. Register installs a global exporter; call it a single time. The returned
// trace.Exporter is also an exporter.Flusher you can flush on shutdown.
func ExampleRegister() {
	stackdriver.Register(stackdriver.Options{
		ProjectID:    "my-gcp-project",
		MetricPrefix: "parapet",
		Location:     "asia-southeast1",
	})

	s := parapet.New()
	s.Use(stackdriver.Trace())
	// s.Use(upstream.SingleHost("10.0.0.1:8080")) — the traced backend.
}

// Trace returns the request-tracing middleware preconfigured with Stackdriver's
// trace-context propagation, so spans join an existing Google Cloud trace carried
// in the X-Cloud-Trace-Context header. The returned *trace.Trace can be tuned
// before wiring (e.g. mark the edge as a public endpoint so client-supplied span
// IDs start a fresh trace rather than being trusted as the parent).
func ExampleTrace() {
	t := stackdriver.Trace()
	t.IsPublicEndpoint = true

	s := parapet.New()
	s.Use(t)
}

// Propagation exposes just the Stackdriver HTTP trace-context format, for use
// with the lower-level trace.Trace middleware when you want to combine it with
// other (non-default) settings.
func ExamplePropagation() {
	s := parapet.New()
	s.Use(&trace.Trace{
		Propagation:      stackdriver.Propagation(),
		IsPublicEndpoint: true,
	})
}
