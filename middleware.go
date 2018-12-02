package parapet

import "net/http"

// Middleware is the http middleware
type Middleware interface {
	ServeHandler(http.Handler) http.Handler
}

// Middlewares type
type Middlewares []Middleware

// Use uses middleware
func (ms *Middlewares) Use(m Middleware) {
	if m == nil {
		return
	}
	*ms = append(*ms, m)
}

// ServeHandler implements middleware interface
func (ms Middlewares) ServeHandler(h http.Handler) http.Handler {
	for i := len(ms); i > 0; i-- {
		h = ms[i-1].ServeHandler(h)
	}
	return h
}
