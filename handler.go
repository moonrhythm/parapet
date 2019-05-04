package parapet

import (
	"net/http"
)

// Handler wraps http handler func with parapet's middleware
type Handler http.HandlerFunc

func (h Handler) ServeHandler(_ http.Handler) http.Handler {
	return http.HandlerFunc(h)
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.HandlerFunc(h)(w, r)
}

var _ Middleware = Handler(nil)
