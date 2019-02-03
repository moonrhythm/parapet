package parapet

import (
	"net"
	"time"
)

type tcpListener struct {
	*net.TCPListener

	KeepAlivePeriod time.Duration
	ModifyConn      []func(conn net.Conn) net.Conn
}

func (ln tcpListener) Accept() (net.Conn, error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return nil, err
	}
	if ln.KeepAlivePeriod > 0 {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(ln.KeepAlivePeriod)
	}

	conn := net.Conn(tc)
	for _, f := range ln.ModifyConn {
		conn = f(conn)
	}

	return conn, nil
}
