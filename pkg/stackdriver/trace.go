package stackdriver

import (
	sdpropagation "go.opencensus.io/exporter/stackdriver/propagation"
	"go.opencensus.io/trace/propagation"

	"github.com/moonrhythm/parapet/pkg/trace"
)

func Propagation() propagation.HTTPFormat {
	return &sdpropagation.HTTPFormat{}
}

// Trace creates new trace middleware with stackdriver propagation
func Trace() *trace.Trace {
	return &trace.Trace{
		Propagation: Propagation(),
	}
}
