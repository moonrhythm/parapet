package authn

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
)

var (
	ErrMissingAuthorization = errors.New("missing authorization")
	ErrInvalidCredentials   = errors.New("invalid credentials")
)

// Basic creates new basic auth middleware
func Basic(username, password string) *BasicAuthenticator {
	return &BasicAuthenticator{
		Authenticate: func(_ *http.Request, u, p string) error {
			ok := u == username &&
				subtle.ConstantTimeCompare([]byte(p), []byte(password)) == 1
			if !ok {
				return ErrInvalidCredentials
			}
			return nil
		},
	}
}

// BasicAuthenticator middleware
type BasicAuthenticator struct {
	Realm        string
	Authenticate func(r *http.Request, username, password string) error
}

// ServeHandler implements middleware interface
func (m BasicAuthenticator) ServeHandler(h http.Handler) http.Handler {
	t := "Basic"
	if m.Realm != "" {
		t += " realm=\"" + url.PathEscape(m.Realm) + "\""
	}

	return Authenticator{
		Type: t,
		Authenticate: func(r *http.Request) error {
			username, password, ok := r.BasicAuth()
			r.Header.Del("Authorization")
			if !ok {
				return ErrMissingAuthorization
			}
			return m.Authenticate(r, username, password)
		},
	}.ServeHandler(h)
}
