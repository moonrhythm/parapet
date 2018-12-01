package upstream

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Upstream middleware
type Upstream struct {
	Target                string
	DialTimeout           time.Duration
	KeepAlive             time.Duration
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
	ResponseHeaderTimeout time.Duration
}

// ServeHandler implements middleware interface
func (m *Upstream) ServeHandler(h http.Handler) http.Handler {
	target, err := url.Parse(m.Target)
	if err != nil {
		panic(err)
	}

	if m.DialTimeout == 0 {
		m.DialTimeout = 30 * time.Second
	}
	if m.KeepAlive == 0 {
		m.KeepAlive = 30 * time.Second
	}
	if m.MaxIdleConns == 0 {
		m.MaxIdleConns = 100
	}
	if m.IdleConnTimeout == 0 {
		m.IdleConnTimeout = 90 * time.Second
	}

	r := httputil.NewSingleHostReverseProxy(target)
	r.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   m.DialTimeout,
			KeepAlive: m.KeepAlive,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          m.MaxIdleConns,
		MaxIdleConnsPerHost:   m.MaxIdleConns,
		IdleConnTimeout:       m.IdleConnTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
		ResponseHeaderTimeout: m.ResponseHeaderTimeout,
	}

	return r
}
