package router

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet"
)

// Router middleware
type Router struct {
	m map[string]parapet.Middleware
}

// New creates new router
func New() *Router {
	return new(Router)
}

// Handle handles middleware
func (m *Router) Handle(pattern string, h parapet.Middleware) {
	if m.m == nil {
		m.m = make(map[string]parapet.Middleware)
	}
	m.m[pattern] = h
	if !strings.HasSuffix(pattern, "/") {
		m.m[pattern+"/"] = h
	}
}

// ServeHandler implements middleware interface
func (m Router) ServeHandler(h http.Handler) http.Handler {
	mux := http.NewServeMux()

	if _, ok := m.m["/"]; !ok {
		mux.Handle("/", h)
	}
	for pattern, m := range m.m {
		mux.Handle(pattern, m.ServeHandler(h))
	}

	return mux
}
