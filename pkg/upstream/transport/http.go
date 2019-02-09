package transport

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// HTTP transport
type HTTP struct {
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
func (t *HTTP) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		if t.TCPKeepAlive == 0 {
			t.TCPKeepAlive = time.Minute
		}
		if t.MaxIdleConns == 0 {
			t.MaxIdleConns = 32
		}
		if t.IdleConnTimeout == 0 {
			t.IdleConnTimeout = 10 * time.Minute
		}
		if t.ResponseHeaderTimeout == 0 {
			t.ResponseHeaderTimeout = 60 * time.Second
		}

		t.h = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   t.DialTimeout,
				KeepAlive: t.TCPKeepAlive,
				DualStack: true,
			}).DialContext,
			DisableKeepAlives:     t.DisableKeepAlives,
			MaxConnsPerHost:       t.MaxConn,
			MaxIdleConnsPerHost:   t.MaxIdleConns,
			IdleConnTimeout:       t.IdleConnTimeout,
			ExpectContinueTimeout: 1 * time.Second,
			DisableCompression:    true,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
	})

	r.URL.Scheme = "http"
	return t.h.RoundTrip(r)
}
