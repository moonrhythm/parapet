package upstream

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/moonrhythm/parapet/pkg/internal/header"
)

const (
	defaultDialTimeout           = 5 * time.Second
	defaultMaxIdleConns          = 32
	defaultIdleConnTimeout       = 10 * time.Minute
	defaultResponseHeaderTimeout = time.Minute
	defaultTLSHandshakeTimeout   = 5 * time.Second
)

// H2CTransport type
//
//nolint:govet
type H2CTransport struct {
	once   sync.Once
	dialer *net.Dialer
	h2     *http2.Transport
	h1     *http.Transport

	HTTP2Transport *http2.Transport
	HTTPTransport  *http.Transport
}

// RoundTrip implement http.RoundTripper
func (t *H2CTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		t.dialer = &net.Dialer{
			Timeout:   defaultDialTimeout,
			KeepAlive: defaultIdleConnTimeout,
		}

		t.h2 = t.HTTP2Transport
		if t.h2 == nil {
			t.h2 = &http2.Transport{
				DisableCompression: true,
			}
		}
		t.h2.AllowHTTP = true
		t.h2.DialTLSContext = func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return t.dialer.DialContext(ctx, network, addr)
		}

		t.h1 = t.HTTPTransport
		if t.h1 == nil {
			t.h1 = &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           t.dialer.DialContext,
				DisableCompression:    true,
				MaxIdleConns:          defaultMaxIdleConns,
				MaxIdleConnsPerHost:   defaultMaxIdleConns,
				IdleConnTimeout:       defaultIdleConnTimeout,
				ResponseHeaderTimeout: defaultResponseHeaderTimeout,
			}
		}
	})

	r.URL.Scheme = "http"

	// Currently Go does not support RFC 8441, downgrade to http1
	if header.Exists(r.Header, header.Upgrade) {
		return t.h1.RoundTrip(r)
	}

	return t.h2.RoundTrip(r)
}

// HTTPTransport type
//
//nolint:govet
type HTTPTransport struct {
	once sync.Once
	h    *http.Transport

	DialTimeout           time.Duration
	TCPKeepAlive          time.Duration
	DisableKeepAlives     bool
	MaxConn               int
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
	ExpectContinueTimeout time.Duration
	ResponseHeaderTimeout time.Duration
}

// RoundTrip implement http.RoundTripper
func (t *HTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		if t.DialTimeout == 0 {
			t.DialTimeout = defaultDialTimeout
		}
		if t.MaxIdleConns == 0 {
			t.MaxIdleConns = defaultMaxIdleConns
		}
		if t.IdleConnTimeout == 0 {
			t.IdleConnTimeout = defaultIdleConnTimeout
		}
		if t.ResponseHeaderTimeout == 0 {
			t.ResponseHeaderTimeout = defaultResponseHeaderTimeout
		}

		t.h = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   t.DialTimeout,
				KeepAlive: t.TCPKeepAlive,
			}).DialContext,
			DisableKeepAlives:     t.DisableKeepAlives,
			MaxConnsPerHost:       t.MaxConn,
			MaxIdleConnsPerHost:   t.MaxIdleConns,
			IdleConnTimeout:       t.IdleConnTimeout,
			ExpectContinueTimeout: t.ExpectContinueTimeout,
			DisableCompression:    true,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
	})

	r.URL.Scheme = "http"
	return t.h.RoundTrip(r)
}

// HTTPSTransport type
//
//nolint:govet
type HTTPSTransport struct {
	once sync.Once
	h    *http.Transport

	DialTimeout           time.Duration
	TCPKeepAlive          time.Duration
	DisableKeepAlives     bool
	MaxConn               int
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
	ExpectContinueTimeout time.Duration
	ResponseHeaderTimeout time.Duration
	TLSClientConfig       *tls.Config
}

// RoundTrip implement http.RoundTripper
func (t *HTTPSTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		if t.DialTimeout == 0 {
			t.DialTimeout = defaultDialTimeout
		}
		if t.MaxIdleConns == 0 {
			t.MaxIdleConns = defaultMaxIdleConns
		}
		if t.IdleConnTimeout == 0 {
			t.IdleConnTimeout = defaultIdleConnTimeout
		}
		if t.ResponseHeaderTimeout == 0 {
			t.ResponseHeaderTimeout = defaultResponseHeaderTimeout
		}
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{
				InsecureSkipVerify: true,
			}
		}

		t.h = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   t.DialTimeout,
				KeepAlive: t.TCPKeepAlive,
			}).DialContext,
			TLSClientConfig:       t.TLSClientConfig,
			DisableKeepAlives:     t.DisableKeepAlives,
			MaxConnsPerHost:       t.MaxConn,
			MaxIdleConnsPerHost:   t.MaxIdleConns,
			IdleConnTimeout:       t.IdleConnTimeout,
			TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
			ExpectContinueTimeout: t.ExpectContinueTimeout,
			DisableCompression:    true,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
	})

	r.URL.Scheme = "https"
	return t.h.RoundTrip(r)
}

// UnixTransport type
//
//nolint:govet
type UnixTransport struct {
	once sync.Once
	h    *http.Transport

	DisableKeepAlives     bool
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
	ExpectContinueTimeout time.Duration
	ResponseHeaderTimeout time.Duration
}

// RoundTrip implement http.RoundTripper
func (t *UnixTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		if t.MaxIdleConns == 0 {
			t.MaxIdleConns = defaultMaxIdleConns
		}
		if t.IdleConnTimeout == 0 {
			t.IdleConnTimeout = defaultIdleConnTimeout
		}
		if t.ResponseHeaderTimeout == 0 {
			t.ResponseHeaderTimeout = defaultResponseHeaderTimeout
		}

		d := &net.Dialer{}
		t.h = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.DialContext(ctx, "unix", strings.TrimSuffix(addr, ":80"))
			},
			DisableKeepAlives:     t.DisableKeepAlives,
			MaxIdleConnsPerHost:   t.MaxIdleConns,
			IdleConnTimeout:       t.IdleConnTimeout,
			ExpectContinueTimeout: t.ExpectContinueTimeout,
			DisableCompression:    true,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
	})

	r.URL.Scheme = "http"
	r.URL.Host = "/" + r.URL.Host
	return t.h.RoundTrip(r)
}

// Transport does RoundTrip dynamically from request's scheme
//
//nolint:govet
type Transport struct {
	once   sync.Once
	dialer *net.Dialer
	httpTr *http.Transport
	h2cTr  *http2.Transport
	unixTr *http.Transport

	DialTimeout           time.Duration
	TCPKeepAlive          time.Duration
	DisableKeepAlives     bool
	MaxConn               int
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
	ExpectContinueTimeout time.Duration
	ResponseHeaderTimeout time.Duration
	DisableCompression    bool
	TLSClientConfig       *tls.Config
}

// RoundTrip implement http.RoundTripper
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		if t.DialTimeout == 0 {
			t.DialTimeout = defaultDialTimeout
		}
		if t.MaxIdleConns == 0 {
			t.MaxIdleConns = defaultMaxIdleConns
		}
		if t.IdleConnTimeout == 0 {
			t.IdleConnTimeout = defaultIdleConnTimeout
		}
		if t.ResponseHeaderTimeout == 0 {
			t.ResponseHeaderTimeout = defaultResponseHeaderTimeout
		}
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{
				InsecureSkipVerify: true,
			}
		}

		t.dialer = &net.Dialer{
			Timeout:   t.DialTimeout,
			KeepAlive: t.TCPKeepAlive,
		}

		t.httpTr = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           t.dialer.DialContext,
			TLSClientConfig:       t.TLSClientConfig,
			DisableKeepAlives:     t.DisableKeepAlives,
			MaxConnsPerHost:       t.MaxConn,
			MaxIdleConnsPerHost:   t.MaxIdleConns,
			IdleConnTimeout:       t.IdleConnTimeout,
			ExpectContinueTimeout: t.ExpectContinueTimeout,
			DisableCompression:    t.DisableCompression,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
		t.h2cTr = &http2.Transport{
			AllowHTTP:          true,
			DisableCompression: t.DisableCompression,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return t.dialer.DialContext(ctx, network, addr)
			},
		}
		t.unixTr = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return t.dialer.DialContext(ctx, "unix", strings.TrimSuffix(addr, ":80"))
			},
			DisableKeepAlives:     t.DisableKeepAlives,
			MaxIdleConnsPerHost:   t.MaxIdleConns,
			IdleConnTimeout:       t.IdleConnTimeout,
			ExpectContinueTimeout: t.ExpectContinueTimeout,
			DisableCompression:    t.DisableCompression,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
	})

	var tr http.RoundTripper
	switch r.URL.Scheme {
	default:
		tr = t.httpTr
	case "h2c":
		tr = t.h2cTr
		r.URL.Scheme = "http"

		// Currently Go does not support RFC 8441, downgrade to http1
		if header.Exists(r.Header, header.Upgrade) {
			tr = t.httpTr
		}
	case "unix":
		tr = t.unixTr
		r.URL.Scheme = "http"
	}

	return tr.RoundTrip(r)
}
