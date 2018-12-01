package parapet

import "net/http"

// Middleware is the http middleware
type Middleware interface {
	ServeHandler(http.Handler) http.Handler
}

// Middlewares type
type Middlewares []Middleware

// ServeHandler implements middleware interface
func (ms Middlewares) ServeHandler(h http.Handler) http.Handler {
	for i := len(ms); i > 0; i-- {
		h = ms[i-1].ServeHandler(h)
	}
	return h
}
