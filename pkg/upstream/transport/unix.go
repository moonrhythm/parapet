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

	DisableKeepAlives     bool
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
	ResponseHeaderTimeout time.Duration
}

// RoundTrip implement http.RoundTripper
func (t *Unix) RoundTrip(r *http.Request) (*http.Response, error) {
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
