package prom

import (
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var reg = prometheus.NewRegistry()

func init() {
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{
		PidFn:        func() (int, error) { return os.Getpid(), nil },
		ReportErrors: true,
	}))
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
