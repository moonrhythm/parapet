package transport

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Unix transport
type Unix struct {
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

// RoundTrip implement http.RoundTripper
func (t *Unix) RoundTrip(r *http.Request) (*http.Response, error) {
	t.once.Do(func() {
		if t.TCPKeepAlive == 0 {
			t.TCPKeepAlive = 10 * time.Minute
		}
		if t.MaxIdleConns == 0 {
			t.MaxIdleConns = 10000
		}
		if t.IdleConnTimeout == 0 {
			t.IdleConnTimeout = 10 * time.Minute
		}

		d := &net.Dialer{
			Timeout:   t.DialTimeout,
			KeepAlive: t.TCPKeepAlive,
			DualStack: true,
		}

		t.h = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.DialContext(ctx, "unix", strings.TrimSuffix(addr, ":80"))
			},
			DisableKeepAlives:     t.DisableKeepAlives,
			MaxIdleConnsPerHost:   t.MaxIdleConns,
			IdleConnTimeout:       t.IdleConnTimeout,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableCompression:    true,
			ResponseHeaderTimeout: t.ResponseHeaderTimeout,
		}
	})

	r.URL.Scheme = "http"
	r.URL.Host = "/" + r.URL.Host
	return t.h.RoundTrip(r)
}
