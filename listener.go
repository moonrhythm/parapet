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

	if ln.KeepAlivePeriod > 0 {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(ln.KeepAlivePeriod)
	}

	return tc, nil
}

type modifyConnListener struct {
	net.Listener

	ModifyConn []func(conn net.Conn) net.Conn
}

func (ln modifyConnListener) Accept() (net.Conn, error) {
	c, err := ln.Listener.Accept()
	if err != nil {
		return nil, err
	}

	for _, f := range ln.ModifyConn {
		c = f(c)
	}

	return c, nil
}
