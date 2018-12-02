package upstream

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

func (m *Upstream) transport(scheme string) *http.Transport {
	// setup default config
	switch scheme {
	case "unix":
		if m.MaxIdleConns == 0 {
			m.MaxIdleConns = 10000
		}
		if m.IdleConnTimeout == 0 {
			m.IdleConnTimeout = 10 * time.Minute
		}
	default:
		if m.TCPKeepAlive == 0 {
			m.TCPKeepAlive = 10 * time.Minute
		}
		if m.MaxIdleConns == 0 {
			m.MaxIdleConns = 100
		}
		if m.IdleConnTimeout == 0 {
			m.IdleConnTimeout = 10 * time.Minute
		}
	}
	if m.DialTimeout == 0 {
		m.DialTimeout = 5 * time.Second
	}

	d := &net.Dialer{
		Timeout:   m.DialTimeout,
		KeepAlive: m.TCPKeepAlive,
		DualStack: true,
	}

	t := &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: d.DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !m.VerifyCA,
		},
		DisableKeepAlives:     m.DisableKeepAlives,
		MaxIdleConns:          m.MaxIdleConns,
		MaxIdleConnsPerHost:   m.MaxIdleConns,
		IdleConnTimeout:       m.IdleConnTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
		ResponseHeaderTimeout: m.ResponseHeaderTimeout,
	}

	t.RegisterProtocol("h2c", &convertHTTPRoundTripper{
		&http2.Transport{
			AllowHTTP:          true,
			DisableCompression: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
	})

	t.RegisterProtocol("unix", &convertHTTPRoundTripper{
		&http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.DialContext(ctx, "unix", strings.TrimSuffix(addr, ":80"))
			},
			DisableKeepAlives:     m.DisableKeepAlives,
			MaxIdleConns:          m.MaxIdleConns,
			MaxIdleConnsPerHost:   m.MaxIdleConns,
			IdleConnTimeout:       m.IdleConnTimeout,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableCompression:    true,
			ResponseHeaderTimeout: m.ResponseHeaderTimeout,
		},
	})
	return t
}

type convertHTTPRoundTripper struct {
	http.RoundTripper
}

func (rt *convertHTTPRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	return rt.RoundTripper.RoundTrip(req)
}
