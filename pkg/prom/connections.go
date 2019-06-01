package prom

import (
	"net"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet"
)

type connections struct {
	once    sync.Once
	vec     *prometheus.GaugeVec
	storage sync.Map
}

var _connections connections

func (p *connections) init() {
	p.once.Do(func() {
		p.vec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "connections",
		}, []string{"state"})
		reg.MustRegister(p.vec)
	})
}

func (p *connections) inc(state http.ConnState) {
	c, err := p.vec.GetMetricWith(prometheus.Labels{"state": state.String()})
	if err != nil {
		return
	}
	c.Inc()
}

func (p *connections) dec(state http.ConnState) {
	c, err := p.vec.GetMetricWith(prometheus.Labels{"state": state.String()})
	if err != nil {
		return
	}
	c.Dec()
}

func (p *connections) connState(conn net.Conn, state http.ConnState) {
	// increase current state
	if state == http.StateNew || state == http.StateActive || state == http.StateIdle {
		p.inc(state)
	}

	// decrease prev state
	if prev, ok := p.storage.Load(conn); ok {
		p.dec(prev.(http.ConnState))
	}

	// terminate state
	if state == http.StateHijacked || state == http.StateClosed {
		p.storage.Delete(conn)
		return
	}

	p.storage.Store(conn, state)
}

// Connections collects connection metrics from server
func Connections(s *parapet.Server) {
	_connections.init()

	s.ConnState = _connections.connState
}
