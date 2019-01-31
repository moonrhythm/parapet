package prom

import (
	"net"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet"
)

// Connections collects connection metrics from server
func Connections(s *parapet.Server) {
	var (
		connTotal  = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: Namespace, Name: "connection_total"})
		connActive = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: Namespace, Name: "connection_active"})
		connIdle   = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: Namespace, Name: "connection_idle"})
	)
	reg.MustRegister(connActive, connIdle)

	storage := sync.Map{}

	s.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connTotal.Inc()
			return
		}

		if prev, ok := storage.Load(conn); ok {
			switch prev.(http.ConnState) {
			case http.StateActive:
				connActive.Dec()
			case http.StateIdle:
				connIdle.Dec()
			}
		}

		switch state {
		case http.StateActive:
			connActive.Inc()
		case http.StateIdle:
			connIdle.Inc()
		case http.StateHijacked, http.StateClosed:
			storage.Delete(conn)
			connTotal.Dec()
			return
		}

		storage.Store(conn, state)
	}
}
