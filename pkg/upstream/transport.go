package upstream

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

func (m *Upstream) newTransport() *http.Transport {
	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   m.DialTimeout,
			KeepAlive: m.TCPKeepAlive,
			DualStack: true,
		}).DialContext,
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

	t.RegisterProtocol("h2c", newH2CTransport())
	return t
}

type h2cTransport struct {
	t *http2.Transport
}

func newH2CTransport() *h2cTransport {
	return &h2cTransport{
		&http2.Transport{
			AllowHTTP:          true,
			DisableCompression: true,
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
	}
}

func (t *h2cTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	return t.t.RoundTrip(req)
}
