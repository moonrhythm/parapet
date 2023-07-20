package stackdriver

import (
	"log"
	"time"

	"contrib.go.opencensus.io/exporter/stackdriver"
	"go.opencensus.io/trace"
	"google.golang.org/api/option"
)

//nolint:govet
type ExporterOptions struct {
	ProjectID               string
	BundleCountThreshold    int
	BundleDelayThreshold    time.Duration
	MonitoringClientOptions []option.ClientOption
	TraceClientOptions      []option.ClientOption
}

func Register(opt *ExporterOptions) trace.Exporter {
	if opt == nil {
		opt = new(ExporterOptions)
	}

	exporter, err := stackdriver.NewExporter(stackdriver.Options{
		ProjectID:               opt.ProjectID,
		BundleCountThreshold:    opt.BundleCountThreshold,
		BundleDelayThreshold:    opt.BundleDelayThreshold,
		MonitoringClientOptions: opt.MonitoringClientOptions,
		TraceClientOptions:      opt.TraceClientOptions,
	})
	if err != nil {
		log.Println("stackdriver: can not create exporter;", err)
		return nil
	}

	trace.RegisterExporter(exporter)

	return exporter
}
