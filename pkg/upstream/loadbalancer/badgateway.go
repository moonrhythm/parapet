package loadbalancer

import (
	"fmt"
	"net/http"
)

type badGateway struct{}

func (badGateway) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("bad gateway")
}
