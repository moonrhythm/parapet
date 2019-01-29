package loadbalancer

import (
	"net/http"
)

// Target is the load balancer target
type Target struct {
	Host      string
	Transport http.RoundTripper
}
