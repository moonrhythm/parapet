package proxyprotocol

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- header builders -------------------------------------------------------

func v1Line(proto, src, dst string, sport, dport int) []byte {
	return []byte(fmt.Sprintf("PROXY %s %s %s %d %d\r\n", proto, src, dst, sport, dport))
}

func v2Header(cmd, family byte, block []byte) []byte {
	var b bytes.Buffer
	b.Write(v2Signature)
	b.WriteByte(v2VersionPROXY<<4 | cmd)
	b.WriteByte(family<<4 | 0x1) // STREAM (TCP)
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(block)))
	b.Write(l[:])
	b.Write(block)
	return b.Bytes()
}

func be16(v uint16) []byte {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	return b[:]
}

func v2Inet(src, dst net.IP, sport, dport uint16, tlv []byte) []byte {
	block := make([]byte, 0, v2AddrLenInet+len(tlv))
	block = append(block, src.To4()...)
	block = append(block, dst.To4()...)
	block = append(block, be16(sport)...)
	block = append(block, be16(dport)...)
	block = append(block, tlv...)
	return v2Header(v2CmdProxy, v2FamInet, block)
}

func v2Inet6(src, dst net.IP, sport, dport uint16) []byte {
	block := make([]byte, 0, v2AddrLenInet6)
	block = append(block, src.To16()...)
	block = append(block, dst.To16()...)
	block = append(block, be16(sport)...)
	block = append(block, be16(dport)...)
	return v2Header(v2CmdProxy, v2FamInet6, block)
}

// --- parseHeader -----------------------------------------------------------

func TestParseHeader(t *testing.T) {
	t.Parallel()

	const payload = "GET / HTTP/1.1\r\nHost: x\r\n\r\n"

	type want struct {
		addr      string // "" means nil (no override)
		err       error  // sentinel to match with ErrorIs; nil = no error
		remainder string // what should still be readable after the header
	}
	cases := []struct {
		name string
		in   []byte
		want want
	}{
		{
			name: "v1 TCP4",
			in:   v1Line("TCP4", "192.0.2.1", "10.0.0.9", 56324, 443),
			want: want{addr: "192.0.2.1:56324", remainder: payload},
		},
		{
			name: "v1 TCP6",
			in:   v1Line("TCP6", "2001:db8::1", "2001:db8::2", 4321, 443),
			want: want{addr: "[2001:db8::1]:4321", remainder: payload},
		},
		{
			name: "v1 UNKNOWN keeps peer",
			in:   []byte("PROXY UNKNOWN\r\n"),
			want: want{addr: "", remainder: payload},
		},
		{
			name: "v1 bad port",
			in:   []byte("PROXY TCP4 192.0.2.1 10.0.0.9 notaport 443\r\n"),
			want: want{err: errMalformed},
		},
		{
			name: "v2 INET",
			in:   v2Inet(net.ParseIP("192.0.2.7"), net.ParseIP("10.0.0.9"), 51000, 443, nil),
			want: want{addr: "192.0.2.7:51000", remainder: payload},
		},
		{
			name: "v2 INET6",
			in:   v2Inet6(net.ParseIP("2001:db8::a"), net.ParseIP("2001:db8::b"), 6000, 443),
			want: want{addr: "[2001:db8::a]:6000", remainder: payload},
		},
		{
			name: "v2 INET with trailing TLV discarded",
			in:   v2Inet(net.ParseIP("192.0.2.7"), net.ParseIP("10.0.0.9"), 51000, 443, []byte{0x03, 0x00, 0x02, 0xAA, 0xBB}),
			want: want{addr: "192.0.2.7:51000", remainder: payload},
		},
		{
			name: "v2 LOCAL keeps peer",
			in:   v2Header(v2CmdLocal, v2FamUnspec, nil),
			want: want{addr: "", remainder: payload},
		},
		{
			name: "v2 UNSPEC keeps peer",
			in:   v2Header(v2CmdProxy, v2FamUnspec, []byte{0x01, 0x02}),
			want: want{addr: "", remainder: payload},
		},
		{
			name: "not a proxy header",
			in:   []byte(payload),
			want: want{err: errNoHeader, remainder: payload},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := append(append([]byte(nil), tc.in...), []byte(payload)...)
			r := bufio.NewReader(bytes.NewReader(in))

			addr, err := parseHeader(r)

			if tc.want.err != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.want.err)
				if tc.want.err == errNoHeader {
					// Nothing consumed: the whole stream is still readable.
					rest, _ := io.ReadAll(r)
					assert.Equal(t, in, rest)
				}
				return
			}

			require.NoError(t, err)
			if tc.want.addr == "" {
				assert.Nil(t, addr)
			} else {
				require.NotNil(t, addr)
				assert.Equal(t, tc.want.addr, addr.String())
			}
			rest, _ := io.ReadAll(r)
			assert.Equal(t, tc.want.remainder, string(rest))
		})
	}
}

func TestParseHeader_v2Truncated(t *testing.T) {
	t.Parallel()
	// AF_INET declared but the address block is short.
	hdr := v2Header(v2CmdProxy, v2FamInet, []byte{1, 2, 3})
	_, err := parseHeader(bufio.NewReader(bytes.NewReader(hdr)))
	assert.ErrorIs(t, err, errMalformed)
}

func TestParseHeader_v1TooLong(t *testing.T) {
	t.Parallel()
	// A complete line (has CRLF) but longer than the v1 maximum.
	line := "PROXY TCP4 192.0.2.1 10.0.0.9 1 1" + strings.Repeat(" ", 100) + "\r\n"
	_, err := parseHeader(bufio.NewReader(bytes.NewReader([]byte(line))))
	assert.ErrorIs(t, err, errMalformed)
}

func TestParseHeader_v1NoNewline(t *testing.T) {
	t.Parallel()
	// "PROXY " prefix then a flood with no newline must not grow unbounded; it
	// overruns the bufio buffer and is rejected as malformed.
	in := append([]byte("PROXY TCP4 "), bytes.Repeat([]byte("9"), 8192)...)
	_, err := parseHeader(bufio.NewReader(bytes.NewReader(in)))
	assert.ErrorIs(t, err, errMalformed)
}

// --- fakeConn --------------------------------------------------------------

type fakeConn struct {
	r       io.Reader
	remote  net.Addr
	written bytes.Buffer
	closed  bool
}

func tcpAddr(ip string, port int) *net.TCPAddr {
	return &net.TCPAddr{IP: net.ParseIP(ip), Port: port}
}

func (c *fakeConn) Read(p []byte) (int, error)       { return c.r.Read(p) }
func (c *fakeConn) Write(p []byte) (int, error)      { return c.written.Write(p) }
func (c *fakeConn) Close() error                     { c.closed = true; return nil }
func (c *fakeConn) LocalAddr() net.Addr              { return tcpAddr("127.0.0.1", 80) }
func (c *fakeConn) RemoteAddr() net.Addr             { return c.remote }
func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }

func newFakeConn(peerIP string, data []byte) *fakeConn {
	return &fakeConn{r: bytes.NewReader(data), remote: tcpAddr(peerIP, 40000)}
}

// --- Modifier --------------------------------------------------------------

func TestModifier_TrustedOverridesRemoteAddr(t *testing.T) {
	t.Parallel()
	const payload = "ping"
	in := append(v1Line("TCP4", "192.0.2.55", "10.0.0.9", 12345, 443), payload...)
	fc := newFakeConn("10.1.2.3", in)

	c := New("10.0.0.0/8").ModifyConnection(fc)

	assert.Equal(t, "192.0.2.55:12345", c.RemoteAddr().String())
	got, err := io.ReadAll(c)
	require.NoError(t, err)
	assert.Equal(t, payload, string(got))
}

func TestModifier_UntrustedPeerPassthrough(t *testing.T) {
	t.Parallel()
	in := append(v1Line("TCP4", "192.0.2.55", "10.0.0.9", 12345, 443), "ping"...)
	fc := newFakeConn("203.0.113.9", in) // not in 10.0.0.0/8

	c := New("10.0.0.0/8").ModifyConnection(fc)

	// Returned untouched: same object, real peer, header NOT consumed.
	assert.Same(t, fc, c)
	assert.Equal(t, "203.0.113.9:40000", c.RemoteAddr().String())
	got, _ := io.ReadAll(c)
	assert.Equal(t, string(in), string(got))
}

func TestModifier_TrustAllWithV2(t *testing.T) {
	t.Parallel()
	const payload = "data"
	in := append(v2Inet(net.ParseIP("198.51.100.7"), net.ParseIP("10.0.0.9"), 33333, 443, nil), payload...)
	fc := newFakeConn("10.9.9.9", in)

	c := New().ModifyConnection(fc) // no CIDRs => trust all

	got, err := io.ReadAll(c)
	require.NoError(t, err)
	assert.Equal(t, payload, string(got))
	assert.Equal(t, "198.51.100.7:33333", c.RemoteAddr().String())
}

func TestModifier_RequireRejectsMissingHeader(t *testing.T) {
	t.Parallel()
	fc := newFakeConn("10.1.2.3", []byte("GET / HTTP/1.1\r\n\r\n"))

	m := New("10.0.0.0/8")
	m.Require = true
	c := m.ModifyConnection(fc)

	_, err := io.ReadAll(c)
	assert.ErrorIs(t, err, errNoHeader)
	// RemoteAddr falls back to the real peer.
	assert.Equal(t, "10.1.2.3:40000", c.RemoteAddr().String())
}

func TestModifier_OptionalPassesThroughMissingHeader(t *testing.T) {
	t.Parallel()
	const req = "GET / HTTP/1.1\r\n\r\n"
	fc := newFakeConn("10.1.2.3", []byte(req))

	c := New("10.0.0.0/8").ModifyConnection(fc) // Require defaults to false

	got, err := io.ReadAll(c)
	require.NoError(t, err)
	assert.Equal(t, req, string(got))
	assert.Equal(t, "10.1.2.3:40000", c.RemoteAddr().String())
}

func TestModifier_UnknownKeepsPeer(t *testing.T) {
	t.Parallel()
	const payload = "x"
	in := append([]byte("PROXY UNKNOWN\r\n"), payload...)
	fc := newFakeConn("10.1.2.3", in)

	c := New("10.0.0.0/8").ModifyConnection(fc)

	assert.Equal(t, "10.1.2.3:40000", c.RemoteAddr().String())
	got, _ := io.ReadAll(c)
	assert.Equal(t, payload, string(got))
}

func TestNew_InvalidCIDRPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { New("not-a-cidr") })
}

// Exercises the wrapper over a real net.Conn (net.Pipe) so the SetReadDeadline
// path and bufio interplay run against a genuine connection.
func TestModifier_OverRealConn(t *testing.T) {
	t.Parallel()
	const payload = "hello-body"
	client, server := net.Pipe()
	defer server.Close()

	go func() {
		defer client.Close()
		_, _ = client.Write(v1Line("TCP4", "192.0.2.200", "10.0.0.9", 9999, 443))
		_, _ = client.Write([]byte(payload))
	}()

	c := New().ModifyConnection(server) // trust all (pipe addr is opaque)
	assert.Equal(t, "192.0.2.200:9999", c.RemoteAddr().String())

	buf := make([]byte, len(payload))
	_, err := io.ReadFull(c, buf)
	require.NoError(t, err)
	assert.Equal(t, payload, string(buf))
}
