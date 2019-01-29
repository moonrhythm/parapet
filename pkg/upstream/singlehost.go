package upstream

import "net/http"

// SingleHost creates new single host upstream
func SingleHost(host string, transport http.RoundTripper) *Upstream {
	return New(&singleHostTransport{
		Host:      host,
		Transport: transport,
	})
}

type singleHostTransport struct {
	Host      string
	Transport http.RoundTripper
}

func (l *singleHostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Host = l.Host
	return l.Transport.RoundTrip(r)
}
