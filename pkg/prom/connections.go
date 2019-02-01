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
	connections := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: Namespace,
		Name:      "connections",
	}, []string{"state"})
	reg.MustRegister(connections)

	inc := func(state http.ConnState) {
		c, err := connections.GetMetricWith(prometheus.Labels{"state": state.String()})
		if err != nil {
			return
		}
		c.Inc()
	}

	dec := func(state http.ConnState) {
		c, err := connections.GetMetricWith(prometheus.Labels{"state": state.String()})
		if err != nil {
			return
		}
		c.Dec()
	}

	var storage sync.Map
	s.ConnState = func(conn net.Conn, state http.ConnState) {
		// increase current state
		if state == http.StateNew || state == http.StateActive || state == http.StateIdle {
			inc(state)
		}

		// decrease prev state
		if prev, ok := storage.Load(conn); ok {
			dec(prev.(http.ConnState))
		}

		// terminate state
		if state == http.StateHijacked || state == http.StateClosed {
			storage.Delete(conn)
			return
		}

		storage.Store(conn, state)
	}
}
