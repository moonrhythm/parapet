package trace

import (
	"net/http"

	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"

	"github.com/moonrhythm/parapet/pkg/header"
)

// New creates new trace middleware
func New() *Trace {
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
	GetStartOptions  func(r *http.Request) trace.StartOptions
}

// ServeHandler implements middleware interface
func (m Trace) ServeHandler(h http.Handler) http.Handler {
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
		GetStartOptions:  m.GetStartOptions,
		IsPublicEndpoint: m.IsPublicEndpoint,
	}
}
