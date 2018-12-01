package parapet

import (
	"net"
	"time"
)

type tcpKeepAliveListener struct {
	*net.TCPListener
	duration time.Duration
}

func (ln tcpKeepAliveListener) Accept() (net.Conn, error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return nil, err
	}
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(ln.duration)
	return tc, nil
}
