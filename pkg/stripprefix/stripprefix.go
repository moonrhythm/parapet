package stripprefix

import "net/http"

// StripPrefix middleware
type StripPrefix struct {
	Prefix string
}

// ServeHandler implements middleware interface
func (m *StripPrefix) ServeHandler(h http.Handler) http.Handler {
	return http.StripPrefix(m.Prefix, h)
}
