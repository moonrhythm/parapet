package prom

import (
	"net"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet"
)

// Networks tracks network io
func Networks(s *parapet.Server) {
	requests := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "network_request_bytes",
	})
	responses := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace,
		Name:      "network_response_bytes",
	})
	reg.MustRegister(requests, responses)

	read := func(n int) {
		requests.Add(float64(n))
	}
	write := func(n int) {
		responses.Add(float64(n))
	}

	s.ModifyConnection(func(conn net.Conn) net.Conn {
		return &connNetTrack{
			Conn:    conn,
			onRead:  read,
			onWrite: write,
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
	c.onRead(n)
	return
}

func (c *connNetTrack) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	c.onWrite(n)
	return
}
