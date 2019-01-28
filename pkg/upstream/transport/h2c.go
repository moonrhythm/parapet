package transport

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// H2C transport
type H2C struct {
	once sync.Once
	h    *http.Transport

	DialTimeout           time.Duration
	Timeout               time.Duration
	TCPKeepAlive          time.Duration
	DisableKeepAlives     bool
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
	ResponseHeaderTimeout time.Duration
}

func (t *H2C) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		if t.TCPKeepAlive == 0 {
			t.TCPKeepAlive = 10 * time.Minute
		}
		if t.MaxIdleConns == 0 {
			t.MaxIdleConns = 100
		}
		if t.IdleConnTimeout == 0 {
			t.IdleConnTimeout = 10 * time.Minute
		}

		t.h = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   t.DialTimeout,
				KeepAlive: t.TCPKeepAlive,
				DualStack: true,
			}).DialContext,
			DisableKeepAlives:     t.DisableKeepAlives,
			MaxIdleConnsPerHost:   t.MaxIdleConns,
			IdleConnTimeout:       t.IdleConnTimeout,
			ExpectContinueTimeout: 1 * time.Second,
			DisableCompression:    true,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
		t.h.RegisterProtocol("http", &http2.Transport{
			AllowHTTP:          true,
			DisableCompression: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		})
	})

	return t.h.RoundTrip(r)
}

func (t *H2C) Scheme() string {
	return "http"
}
