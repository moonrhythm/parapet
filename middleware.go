package parapet

import "net/http"

// Middleware is the http middleware
type Middleware interface {
	ServeHandler(http.Handler) http.Handler
}
