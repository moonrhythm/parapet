package stripprefix

import "net/http"

// StripPrefix middleware
type StripPrefix struct {
	Prefix string
}

// New creates new strip prefix middleware
func New(prefix string) *StripPrefix {
	return &StripPrefix{Prefix: prefix}
}

// ServeHandler implements middleware interface
func (m *StripPrefix) ServeHandler(h http.Handler) http.Handler {
	return http.StripPrefix(m.Prefix, h)
}
