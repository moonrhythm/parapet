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
)

const (
	defaultDialTimeout           = 5 * time.Second
	defaultMaxIdleConns          = 32
	defaultTCPKeepAlive          = time.Minute
	defaultIdleConnTimeout       = 10 * time.Minute
	defaultResponseHeaderTimeout = time.Minute
	defaultExpectContinueTimeout = time.Second
	defaultTLSHandshakeTimeout   = 5 * time.Second
)

// H2CTransport type
type H2CTransport struct {
	once sync.Once
	h    *http2.Transport
}

// RoundTrip implement http.RoundTripper
func (t *H2CTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		t.h = &http2.Transport{
			AllowHTTP:          true,
			DisableCompression: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		}
	})

	r.URL.Scheme = "http"
	return t.h.RoundTrip(r)
}

// HTTPTransport type
type HTTPTransport struct {
	once sync.Once
	h    *http.Transport

	DialTimeout           time.Duration
	TCPKeepAlive          time.Duration
	DisableKeepAlives     bool
	MaxConn               int
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
	ResponseHeaderTimeout time.Duration
}

// RoundTrip implement http.RoundTripper
func (t *HTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		if t.DialTimeout == 0 {
			t.DialTimeout = defaultDialTimeout
		}
		if t.TCPKeepAlive == 0 {
			t.TCPKeepAlive = defaultTCPKeepAlive
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
			ExpectContinueTimeout: defaultExpectContinueTimeout,
			DisableCompression:    true,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
	})

	r.URL.Scheme = "http"
	return t.h.RoundTrip(r)
}

// HTTPSTransport type
type HTTPSTransport struct {
	once sync.Once
	h    *http.Transport

	DialTimeout           time.Duration
	TCPKeepAlive          time.Duration
	DisableKeepAlives     bool
	MaxConn               int
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
	ResponseHeaderTimeout time.Duration
	TLSClientConfig       *tls.Config
}

// RoundTrip implement http.RoundTripper
func (t *HTTPSTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		if t.DialTimeout == 0 {
			t.DialTimeout = defaultDialTimeout
		}
		if t.TCPKeepAlive == 0 {
			t.TCPKeepAlive = defaultTCPKeepAlive
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
			ExpectContinueTimeout: defaultExpectContinueTimeout,
			DisableCompression:    true,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
	})

	r.URL.Scheme = "https"
	return t.h.RoundTrip(r)
}

// UnixTransport type
type UnixTransport struct {
	once sync.Once
	h    *http.Transport

	DisableKeepAlives     bool
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
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
			ExpectContinueTimeout: defaultExpectContinueTimeout,
			DisableCompression:    true,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
	})

	r.URL.Scheme = "http"
	r.URL.Host = "/" + r.URL.Host
	return t.h.RoundTrip(r)
}
