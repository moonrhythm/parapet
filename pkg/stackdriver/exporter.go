package stackdriver

import (
	"log"

	"contrib.go.opencensus.io/exporter/stackdriver"
	"go.opencensus.io/trace"
)

func Register(opt stackdriver.Options) trace.Exporter {
	exporter, err := stackdriver.NewExporter(opt)
	if err != nil {
		log.Println("stackdriver: can not create exporter;", err)
		return nil
	}

	trace.RegisterExporter(exporter)

	return exporter
}
