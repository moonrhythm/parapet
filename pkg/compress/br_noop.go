// +build !cbrotli

package compress

import "net/http"

// Br creates noop middleware
func Br() *Noop {
	return new(Noop)
}

// Noop middleware
type Noop struct{}

// ServeHandler implements middleware interface
func (m *Noop) ServeHandler(h http.Handler) http.Handler {
	return h
}
