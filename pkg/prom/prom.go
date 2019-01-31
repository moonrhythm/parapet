package prom

import (
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var reg = prometheus.NewRegistry()

// Namespace is the prometheus namespace
var Namespace = "parapet"

func init() {
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{
		PidFn:        func() (int, error) { return os.Getpid(), nil },
		ReportErrors: true,
	}))
}

// Registry returns prometheus registry
func Registry() *prometheus.Registry {
	return reg
}

// Handler returns prom handler
func Handler() http.Handler {
	return promhttp.InstrumentMetricHandler(
		reg,
		promhttp.HandlerFor(reg, promhttp.HandlerOpts{
			DisableCompression: true,
		}),
	)
}

// Start starts prom server
func Start(addr string) error {
	return (&http.Server{
		Addr:         addr,
		ReadTimeout:  30 * time.Second,
		IdleTimeout:  120 * time.Second,
		WriteTimeout: 30 * time.Second,
		Handler:      Handler(),
	}).ListenAndServe()
}
