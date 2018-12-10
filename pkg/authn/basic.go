package authn

import (
	"crypto/subtle"
	"net/http"
	"net/url"
)

// Basic creates new basic auth middleware
func Basic(username, password string) *BasicAuthenticator {
	return &BasicAuthenticator{
		Authenticator: func(u, p string) bool {
			return u == username &&
				subtle.ConstantTimeCompare([]byte(p), []byte(password)) == 1
		},
	}
}

// BasicAuthenticator middleware
type BasicAuthenticator struct {
	Realm         string
	Authenticator func(username, password string) bool
}

// ServeHandler implements middleware interface
func (m *BasicAuthenticator) ServeHandler(h http.Handler) http.Handler {
	t := "Basic"
	if m.Realm != "" {
		t += " realm=\"" + url.PathEscape(m.Realm) + "\""
	}

	return (&Authenticator{
		Type: t,
		Authenticator: func(r *http.Request) bool {
			username, password, ok := r.BasicAuth()
			if !ok {
				return false
			}
			return m.Authenticator(username, password)
		},
	}).ServeHandler(h)
}
