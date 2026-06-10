package upstream_test

import (
	"context"
	"log"
	"net"
	"time"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/upstream"
)

// Wrap the transport's dialer to observe dial failures (e.g. to log them or
// emit a metric) without changing how the connection is made. DialContext is an
// optional seam: leaving it nil keeps the default net.Dialer behavior. When it
// is set, the custom dialer owns its own timeouts — DialTimeout is ignored.
func ExampleHTTPTransport_dialContext() {
	base := &net.Dialer{Timeout: 2 * time.Second}

	tr := &upstream.HTTPTransport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := base.DialContext(ctx, network, addr)
			if err != nil {
				log.Printf("upstream dial failed: addr=%s err=%v", addr, err)
				return nil, err
			}
			return conn, nil
		},
	}

	s := parapet.New()
	s.Use(upstream.SingleHost("10.0.0.1:8080", tr))
}
