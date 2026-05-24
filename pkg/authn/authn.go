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

	// ShareValueSlice writes the WWW-Authenticate value from a single slice
	// shared across requests instead of allocating one per unauthenticated
	// response. Type is fixed at construction, so this is safe as long as
	// nothing mutates the response header value slice in place. Off by
	// default; see header.SetShared.
	ShareValueSlice bool
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

	// When sharing is enabled, WWW-Authenticate is fixed at construction, so
	// build the value slice once and share it across unauthenticated responses.
	var wwwAuthenticate []string
	if m.Type != "" && m.ShareValueSlice {
		wwwAuthenticate = []string{m.Type}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := m.Authenticate(r); err != nil {
			if m.Type != "" {
				if m.ShareValueSlice {
					header.SetShared(w.Header(), header.WWWAuthenticate, wwwAuthenticate)
				} else {
					header.Set(w.Header(), header.WWWAuthenticate, m.Type)
				}
			}
			m.Forbidden(w, r, err)
			return
		}

		h.ServeHTTP(w, r)
	})
}
