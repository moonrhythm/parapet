package proxyprotocol

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
)

// v2Signature is the 12-byte block that begins every PROXY protocol v2 header.
var v2Signature = []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}

var v1Prefix = []byte("PROXY ")

// errNoHeader reports that the stream does not begin with a PROXY header. When
// it is returned, nothing has been consumed from the reader.
var errNoHeader = errors.New("proxyprotocol: no PROXY header")

// errMalformed reports a PROXY header that was identified but could not be
// parsed. Bytes may have been consumed, so the connection can no longer serve a
// request and should be dropped.
var errMalformed = errors.New("proxyprotocol: malformed PROXY header")

// maxV1Line is the maximum length of a v1 header line including CRLF (RFC: 108).
const maxV1Line = 108

// parseHeader reads a PROXY protocol v1 or v2 header from r and returns the
// real client (source) address.
//
//   - (addr, nil): a PROXY command carrying a TCP/UDP source address.
//   - (nil, nil):  a header with no usable source — v1 UNKNOWN, v2 LOCAL, or an
//     AF_UNSPEC/AF_UNIX v2 address. The header is consumed; keep the real peer.
//   - (nil, errNoHeader): not a PROXY header; nothing was consumed.
//   - (nil, errMalformed / io error): a broken header; bytes were consumed.
func parseHeader(r *bufio.Reader) (net.Addr, error) {
	// Peek does not consume, so a non-PROXY stream is left intact for the caller
	// to pass through untouched.
	buf, _ := r.Peek(len(v2Signature))
	switch {
	case len(buf) >= len(v2Signature) && bytes.Equal(buf, v2Signature):
		return parseV2(r)
	case len(buf) >= len(v1Prefix) && bytes.Equal(buf[:len(v1Prefix)], v1Prefix):
		return parseV1(r)
	default:
		return nil, errNoHeader
	}
}

// parseV1 parses a human-readable v1 header line:
//
//	PROXY TCP4 <src> <dst> <sport> <dport>\r\n
//	PROXY TCP6 <src> <dst> <sport> <dport>\r\n
//	PROXY UNKNOWN ...\r\n
func parseV1(r *bufio.Reader) (net.Addr, error) {
	// ReadSlice is bounded by the bufio buffer, so a trusted peer that never
	// sends a newline can't grow an unbounded line (it yields ErrBufferFull).
	line, err := r.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return nil, errMalformed
		}
		return nil, err
	}
	if len(line) > maxV1Line || !bytes.HasSuffix(line, []byte("\r\n")) {
		return nil, errMalformed
	}

	fields := strings.Split(string(line[:len(line)-2]), " ")
	if len(fields) < 2 || fields[0] != "PROXY" {
		return nil, errMalformed
	}

	switch fields[1] {
	case "TCP4", "TCP6":
		if len(fields) != 6 {
			return nil, errMalformed
		}
		ip := net.ParseIP(fields[2])
		port, err := strconv.Atoi(fields[4])
		if ip == nil || err != nil || port < 0 || port > 65535 {
			return nil, errMalformed
		}
		return &net.TCPAddr{IP: ip, Port: port}, nil
	case "UNKNOWN":
		// Connection from an unknown source (e.g. a health check); keep the real
		// peer but still consume the header.
		return nil, nil
	default:
		return nil, errMalformed
	}
}

// PROXY protocol v2 constants.
const (
	v2VersionPROXY = 0x2 // high nibble of byte 12

	v2CmdLocal = 0x0 // health check / no address
	v2CmdProxy = 0x1 // addresses follow

	v2FamUnspec = 0x0
	v2FamInet   = 0x1 // AF_INET  (IPv4)
	v2FamInet6  = 0x2 // AF_INET6 (IPv6)
	v2FamUnix   = 0x3

	v2AddrLenInet  = 12 // 4 + 4 + 2 + 2
	v2AddrLenInet6 = 36 // 16 + 16 + 2 + 2
)

// parseV2 parses the binary v2 header. The 16-byte fixed header (12-byte
// signature + version/command + family/protocol + 2-byte length) is followed by
// length bytes of address block and optional TLVs, which are discarded.
func parseV2(r *bufio.Reader) (net.Addr, error) {
	var hdr [16]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}

	verCmd := hdr[12]
	if verCmd>>4 != v2VersionPROXY {
		return nil, errMalformed
	}
	cmd := verCmd & 0x0F
	family := hdr[13] >> 4
	length := int(binary.BigEndian.Uint16(hdr[14:16]))

	switch cmd {
	case v2CmdLocal:
		// No address; the real peer stands. Discard any payload.
		return nil, discard(r, length)
	case v2CmdProxy:
		// handled below
	default:
		return nil, errMalformed
	}

	var src net.Addr
	consumed := 0
	switch family {
	case v2FamInet:
		if length < v2AddrLenInet {
			return nil, errMalformed
		}
		var b [v2AddrLenInet]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return nil, err
		}
		consumed = v2AddrLenInet
		src = &net.TCPAddr{
			IP:   append(net.IP(nil), b[0:4]...),
			Port: int(binary.BigEndian.Uint16(b[8:10])),
		}
	case v2FamInet6:
		if length < v2AddrLenInet6 {
			return nil, errMalformed
		}
		var b [v2AddrLenInet6]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return nil, err
		}
		consumed = v2AddrLenInet6
		src = &net.TCPAddr{
			IP:   append(net.IP(nil), b[0:16]...),
			Port: int(binary.BigEndian.Uint16(b[32:34])),
		}
	case v2FamUnspec, v2FamUnix:
		// No IP override; consume the whole block below.
	default:
		return nil, errMalformed
	}

	// Discard the remainder of the address block (TLVs, or the full body for
	// AF_UNSPEC/AF_UNIX).
	if err := discard(r, length-consumed); err != nil {
		return nil, err
	}
	return src, nil
}

func discard(r *bufio.Reader, n int) error {
	if n <= 0 {
		return nil
	}
	_, err := r.Discard(n)
	return err
}
