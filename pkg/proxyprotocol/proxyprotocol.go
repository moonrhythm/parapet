// Package proxyprotocol reads the HAProxy PROXY protocol (v1 and v2) header
// from accepted connections and rewrites their RemoteAddr to the real client
// address. Mount it on a Server with Server.ModifyConnection so that middleware
// reading the client IP — ratelimit, waf, logger, and the X-Forwarded-* trust
// logic — sees the actual client instead of the L4 load balancer in front.
//
//	m := proxyprotocol.New("10.0.0.0/8") // your load balancer's range
//	s := parapet.NewFrontend()
//	s.ModifyConnection(m.ModifyConnection)
//
// Only a connection whose immediate peer is within the trusted CIDRs may set a
// client address; a direct attacker outside them is passed through untouched
// and cannot spoof a client IP. The header is parsed lazily on the connection's
// first read, off the accept loop.
package proxyprotocol

import (
	"bufio"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

// DefaultHeaderTimeout bounds how long a trusted connection has to deliver its
// PROXY header before the read fails.
const DefaultHeaderTimeout = 10 * time.Second

// Modifier wraps accepted connections to honor the PROXY protocol. Create it
// with New and pass its ModifyConnection method to Server.ModifyConnection.
//
//nolint:govet
type Modifier struct {
	trusted []*net.IPNet

	// Require rejects a trusted connection that does not begin with a valid
	// PROXY header (its first read fails, so the server drops it). By default a
	// trusted connection without a header is passed through with its real peer
	// address — enable Require when every connection from the load balancer is
	// guaranteed to carry the header.
	Require bool

	// HeaderTimeout bounds reading the PROXY header from a trusted connection.
	// Zero uses DefaultHeaderTimeout; a negative value disables the deadline.
	HeaderTimeout time.Duration
}

// New creates a Modifier that trusts the given load-balancer CIDRs to supply a
// client address via the PROXY header. It panics on an invalid CIDR, matching
// parapet's fail-fast trust configuration — a silently-empty trust list is a
// security footgun.
//
// With no CIDRs every peer is trusted; use that only when the listener is
// reachable exclusively through your load balancer.
func New(trustedCIDRs ...string) *Modifier {
	trusted := make([]*net.IPNet, 0, len(trustedCIDRs))
	for _, s := range trustedCIDRs {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic("proxyprotocol: invalid CIDR " + strconv.Quote(s) + ": " + err.Error())
		}
		trusted = append(trusted, n)
	}
	return &Modifier{trusted: trusted}
}

// ModifyConnection wraps c so its PROXY header (if any) is parsed on first read.
// Pass it to Server.ModifyConnection. It performs no I/O itself, so it never
// blocks the accept loop.
func (m *Modifier) ModifyConnection(c net.Conn) net.Conn {
	if !m.trusts(c.RemoteAddr()) {
		// Untrusted peer: leave the connection (and its bytes) untouched.
		return c
	}
	return &conn{
		Conn:          c,
		r:             bufio.NewReader(c),
		require:       m.Require,
		headerTimeout: m.headerTimeout(),
	}
}

func (m *Modifier) headerTimeout() time.Duration {
	if m.HeaderTimeout == 0 {
		return DefaultHeaderTimeout
	}
	return m.HeaderTimeout
}

func (m *Modifier) trusts(addr net.Addr) bool {
	if len(m.trusted) == 0 {
		return true
	}
	ip := addrIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range m.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.TCPAddr:
		return v.IP
	case *net.UDPAddr:
		return v.IP
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

// conn lazily reads a PROXY header from the wrapped connection on the first Read
// or RemoteAddr call, then transparently serves the remaining bytes.
//
//nolint:govet
type conn struct {
	net.Conn
	r             *bufio.Reader
	once          sync.Once
	remoteAddr    net.Addr // overridden client address, nil until parsed/none
	parseErr      error
	require       bool
	headerTimeout time.Duration
}

func (c *conn) parse() {
	if c.headerTimeout > 0 {
		// Bound the header read on the underlying conn, then clear it so the
		// HTTP server can manage its own per-request deadlines.
		_ = c.SetReadDeadline(time.Now().Add(c.headerTimeout))
		defer c.SetReadDeadline(time.Time{})
	}

	src, err := parseHeader(c.r)
	switch {
	case errors.Is(err, errNoHeader):
		// Not a PROXY header. The bytes are still buffered for passthrough.
		if c.require {
			c.parseErr = errNoHeader
		}
	case err != nil:
		// Identified but broken, or an I/O error: the stream is desynced.
		c.parseErr = err
	default:
		// src is nil for LOCAL/UNKNOWN/UNSPEC — keep the real peer.
		c.remoteAddr = src
	}
}

func (c *conn) Read(p []byte) (int, error) {
	c.once.Do(c.parse)
	if c.parseErr != nil {
		return 0, c.parseErr
	}
	return c.r.Read(p)
}

func (c *conn) RemoteAddr() net.Addr {
	c.once.Do(c.parse)
	if c.remoteAddr != nil {
		return c.remoteAddr
	}
	return c.Conn.RemoteAddr()
}
