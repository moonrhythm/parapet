package prom

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet"
)

// Requests collects request count
func Requests() parapet.Middleware {
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "request_total",
	})
	reg.MustRegister(counter)

	return parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Inc()
			h.ServeHTTP(w, r)
		})
	})
}
