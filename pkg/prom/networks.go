package prom

import (
	"net"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet"
)

type networks struct {
	once      sync.Once
	requests  prometheus.Counter
	responses prometheus.Counter
}

var _networks networks

func (p *networks) init() {
	p.once.Do(func() {
		p.requests = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "network_request_bytes",
		})
		p.responses = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "network_response_bytes",
		})
		reg.MustRegister(p.requests, p.responses)
	})
}

func (p *networks) read(n int) {
	p.requests.Add(float64(n))
}

func (p *networks) write(n int) {
	p.responses.Add(float64(n))
}

// Networks tracks network io
func Networks(s *parapet.Server) {
	_networks.init()

	s.ModifyConnection(func(conn net.Conn) net.Conn {
		return &connNetTrack{
			Conn:    conn,
			onRead:  _networks.read,
			onWrite: _networks.write,
		}
	})
}

type connNetTrack struct {
	net.Conn

	onRead  func(n int)
	onWrite func(n int)
}

func (c *connNetTrack) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if n > 0 {
		c.onRead(n)
	}
	return
}

func (c *connNetTrack) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if n > 0 {
		c.onWrite(n)
	}
	return
}
