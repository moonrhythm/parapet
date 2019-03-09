package parapet

import "net/http"

// Endpoint is the http handler that implement middleware interface
type Endpoint struct {
	http.Handler
}

// ServeHandler implements middleware
func (ep Endpoint) ServeHandler(http.Handler) http.Handler {
	return ep
}

// EndpointFunc is the http handler func that implement middleware interface
type EndpointFunc http.HandlerFunc

// ServeHandler implements middleware
func (ep EndpointFunc) ServeHandler(http.Handler) http.Handler {
	return Endpoint{http.HandlerFunc(ep)}
}
