package parapet

import (
	"net"
	"time"
)

type tcpListener struct {
	*net.TCPListener

	KeepAlivePeriod time.Duration
}

func (ln tcpListener) Accept() (net.Conn, error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return nil, err
	}
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(ln.KeepAlivePeriod)
	return tc, nil
}
