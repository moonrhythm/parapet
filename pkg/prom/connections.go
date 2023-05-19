package prom

import (
	"net"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet"
)

//nolint:govet
type connections struct {
	once    sync.Once
	vec     *prometheus.GaugeVec
	gauge   map[http.ConnState]prometheus.Gauge
	storage sync.Map
}

var _connections connections

func (p *connections) init() {
	p.once.Do(func() {
		p.vec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "connections",
		}, []string{"state"})
		p.gauge = map[http.ConnState]prometheus.Gauge{
			http.StateNew:    p.vec.WithLabelValues(http.StateNew.String()),
			http.StateActive: p.vec.WithLabelValues(http.StateActive.String()),
			http.StateIdle:   p.vec.WithLabelValues(http.StateIdle.String()),
		}
		reg.MustRegister(p.vec)
	})
}

func (p *connections) inc(state http.ConnState) {
	if p.gauge == nil {
		return
	}
	p.gauge[state].Inc()
}

func (p *connections) dec(state http.ConnState) {
	if p.gauge == nil {
		return
	}
	p.gauge[state].Dec()
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
