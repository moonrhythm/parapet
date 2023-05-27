package authn

import (
	"net/http"

	"github.com/moonrhythm/parapet/pkg/header"
)

// Authenticator middleware
//
//nolint:govet
type Authenticator struct {
	Type         string
	Authenticate func(*http.Request) error
	Forbidden    func(w http.ResponseWriter, r *http.Request, err error)
}

// ServeHandler implements middleware interface
func (m Authenticator) ServeHandler(h http.Handler) http.Handler {
	if m.Authenticate == nil {
		return h
	}
	if m.Forbidden == nil {
		m.Forbidden = func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := m.Authenticate(r); err != nil {
			if m.Type != "" {
				header.Set(w.Header(), header.WWWAuthenticate, m.Type)
			}
			m.Forbidden(w, r, err)
			return
		}

		h.ServeHTTP(w, r)
	})
}
