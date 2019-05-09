package authn

import (
	"net/http"
)

// Authenticator middleware
type Authenticator struct {
	Type         string
	Authenticate func(*http.Request) bool
	Forbidden    http.Handler
}

// ServeHandler implements middleware interface
func (m Authenticator) ServeHandler(h http.Handler) http.Handler {
	if m.Authenticate == nil {
		return h
	}
	if m.Forbidden == nil {
		m.Forbidden = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.Authenticate(r) {
			if m.Type != "" {
				w.Header().Set("WWW-Authenticate", m.Type)
			}
			m.Forbidden.ServeHTTP(w, r)
			return
		}

		h.ServeHTTP(w, r)
	})
}
