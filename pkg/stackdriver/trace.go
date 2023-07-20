package stackdriver

import (
	"net/http"

	sdpropagation "go.opencensus.io/exporter/stackdriver/propagation"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"

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
	Propagation      propagation.HTTPFormat
	IsPublicEndpoint bool
	FormatSpanName   func(r *http.Request) string
	StartOptions     trace.StartOptions
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

	return &ochttp.Handler{
		Handler:          h,
		Propagation:      m.Propagation,
		FormatSpanName:   m.FormatSpanName,
		StartOptions:     m.StartOptions,
		IsPublicEndpoint: m.IsPublicEndpoint,
	}
}
