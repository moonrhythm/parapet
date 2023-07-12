package stackdriver

import (
	"log"
	"net/http"
	"time"

	"contrib.go.opencensus.io/exporter/stackdriver"
	sdpropagation "go.opencensus.io/exporter/stackdriver/propagation"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
	"google.golang.org/api/option"

	"github.com/moonrhythm/parapet/pkg/header"
)

// NewTrace creates new stack driver trace middleware
func NewTrace() *Trace {
	return new(Trace)
}

// Trace middleware
//
//nolint:govet
type Trace struct {
	ProjectID               string
	Propagation             propagation.HTTPFormat
	BundleCountThreshold    int
	BundleDelayThreshold    time.Duration
	IsPublicEndpoint        bool
	FormatSpanName          func(r *http.Request) string
	StartOptions            trace.StartOptions
	MonitoringClientOptions []option.ClientOption
	TraceClientOptions      []option.ClientOption
}

// ServeHandler implements middleware interface
func (m Trace) ServeHandler(h http.Handler) http.Handler {
	if m.Propagation == nil {
		m.Propagation = &sdpropagation.HTTPFormat{}
	}
	if m.FormatSpanName == nil {
		m.FormatSpanName = func(r *http.Request) string {
			proto := header.Get(r.Header, header.XForwardedProto)
			return proto + "://" + r.Host + r.RequestURI
		}
	}
	if m.StartOptions.SpanKind == trace.SpanKindUnspecified {
		m.StartOptions.SpanKind = trace.SpanKindServer
	}

	exporter, err := stackdriver.NewExporter(stackdriver.Options{
		ProjectID:               m.ProjectID,
		BundleCountThreshold:    m.BundleCountThreshold,
		BundleDelayThreshold:    m.BundleDelayThreshold,
		MonitoringClientOptions: m.MonitoringClientOptions,
		TraceClientOptions:      m.TraceClientOptions,
	})
	if err != nil {
		log.Println("stackdriver/trace:", err)
		return h
	}

	trace.RegisterExporter(exporter)

	return &ochttp.Handler{
		Handler:          h,
		Propagation:      m.Propagation,
		FormatSpanName:   m.FormatSpanName,
		StartOptions:     m.StartOptions,
		IsPublicEndpoint: m.IsPublicEndpoint,
	}
}
