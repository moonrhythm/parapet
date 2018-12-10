package authn

import (
	"net/http"
)

// Authenticator middleware
type Authenticator struct {
	Authenticate  string
	Authenticator func(*http.Request) bool
}

// ServeHandler implements middleware interface
func (m *Authenticator) ServeHandler(h http.Handler) http.Handler {
	if m.Authenticator == nil {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.Authenticator(r) {
			if m.Authenticate != "" {
				w.Header().Set("WWW-Authenticate", m.Authenticate)
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		h.ServeHTTP(w, r)
	})
}
