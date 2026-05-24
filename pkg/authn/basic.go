package authn

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"

	"github.com/moonrhythm/parapet/pkg/header"
)

var (
	ErrMissingAuthorization = errors.New("missing authorization")
	ErrInvalidCredentials   = errors.New("invalid credentials")
)

// Basic creates new basic auth middleware
func Basic(username, password string) *BasicAuthenticator {
	expectedUser := []byte(username)
	expectedPass := []byte(password)
	return &BasicAuthenticator{
		Authenticate: func(_ *http.Request, u, p string) error {
			// Compare both fields in constant time and AND the results, so
			// that timing reveals neither which field mismatched nor how many
			// leading bytes matched.
			userOK := subtle.ConstantTimeCompare([]byte(u), expectedUser)
			passOK := subtle.ConstantTimeCompare([]byte(p), expectedPass)
			if userOK&passOK != 1 {
				return ErrInvalidCredentials
			}
			return nil
		},
	}
}

// BasicAuthenticator middleware
//
//nolint:govet
type BasicAuthenticator struct {
	Realm        string
	Authenticate func(r *http.Request, username, password string) error

	// ShareValueSlice writes the WWW-Authenticate value from a single slice
	// shared across requests instead of allocating one per unauthenticated
	// response. The value is fixed at construction (it depends only on Realm),
	// so this is safe as long as nothing mutates the response header value
	// slice in place. Off by default; see header.SetShared.
	ShareValueSlice bool
}

// ServeHandler implements middleware interface
func (m BasicAuthenticator) ServeHandler(h http.Handler) http.Handler {
	t := "Basic"
	if m.Realm != "" {
		t += " realm=\"" + url.PathEscape(m.Realm) + "\""
	}

	return Authenticator{
		Type:            t,
		ShareValueSlice: m.ShareValueSlice,
		Authenticate: func(r *http.Request) error {
			username, password, ok := r.BasicAuth()
			header.Del(r.Header, header.Authorization)
			if !ok {
				return ErrMissingAuthorization
			}
			return m.Authenticate(r, username, password)
		},
	}.ServeHandler(h)
}
