package router

import (
	"net/http"

	"github.com/moonrhythm/parapet"
)

// Router middleware
type Router struct {
	m map[string]parapet.Middleware
}

// Handle handles middleware
func (m *Router) Handle(pattern string, h parapet.Middleware) {
	if m.m == nil {
		m.m = make(map[string]parapet.Middleware)
	}
	m.m[pattern] = h
}

// ServeHandler implements middleware interface
func (m *Router) ServeHandler(h http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", h)
	for pattern, m := range m.m {
		mux.Handle(pattern, m.ServeHandler(h))
	}

	return mux
}
