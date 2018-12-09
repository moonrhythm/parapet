package trace

import (
	"log"
	"net/http"

	"contrib.go.opencensus.io/exporter/stackdriver"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
)

// New creates new trace middleware
func New() *Trace {
	return &Trace{}
}

// Trace middleware
type Trace struct {
	ProjectID        string
	IsPublicEndpoint bool
}

// ServeHandler implements middleware interface
func (m *Trace) ServeHandler(h http.Handler) http.Handler {
	exporter, err := stackdriver.NewExporter(stackdriver.Options{
		ProjectID: m.ProjectID,
	})
	if err != nil {
		log.Println("stackdriver/trace:", err)
		return h
	}

	trace.RegisterExporter(exporter)

	return &ochttp.Handler{
		Handler: h,
		StartOptions: trace.StartOptions{
			Sampler:  trace.AlwaysSample(),
			SpanKind: trace.SpanKindServer,
		},
		IsPublicEndpoint: m.IsPublicEndpoint,
	}
}
