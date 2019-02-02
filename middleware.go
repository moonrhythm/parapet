package parapet

import "net/http"

// Block is the middleware block
type Block interface {
	Middleware
	Use(Middleware)
}

// Middleware is the http middleware
type Middleware interface {
	ServeHandler(http.Handler) http.Handler
}

// MiddlewareFunc is the adapter type for Middleware
type MiddlewareFunc func(http.Handler) http.Handler

// ServeHandler calls f
func (f MiddlewareFunc) ServeHandler(h http.Handler) http.Handler {
	return f(h)
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

// Conditional returns condition for given request
type Conditional func(r *http.Request) bool
