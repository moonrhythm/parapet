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
