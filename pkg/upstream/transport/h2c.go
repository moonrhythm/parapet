package transport

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"

	"golang.org/x/net/http2"
)

// H2C transport
type H2C struct {
	once sync.Once
	h    *http2.Transport
}

// RoundTrip implement http.RoundTripper
func (t *H2C) RoundTrip(r *http.Request) (*http.Response, error) {
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
