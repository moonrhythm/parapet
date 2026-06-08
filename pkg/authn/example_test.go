package authn_test

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/authn"
)

// Verify HS256 bearer tokens signed with a shared secret, requiring matching
// iss/aud claims. Algorithms are pinned with this package's constants, so no
// JOSE library is imported.
func ExampleJWT() {
	m := authn.JWT([]byte("0123456789abcdef0123456789abcdef"), authn.HS256)
	m.Issuer = "https://issuer.example.com"
	m.Audience = "my-api"

	s := parapet.New()
	s.Use(m)
}

// Verify asymmetric tokens against the issuer's public key. key may be an
// *rsa.PublicKey, *ecdsa.PublicKey or ed25519.PublicKey — typically parsed from
// PEM with x509.ParsePKIXPublicKey. Pin the matching algorithm(s).
func ExampleJWT_publicKey() {
	var pub *rsa.PublicKey // e.g. from x509.ParsePKIXPublicKey

	s := parapet.New()
	s.Use(authn.JWT(pub, authn.RS256))
}

// Read the verified claims that authn.JWT placed on the request context from a
// downstream handler.
func ExampleJWTClaimsFromContext() {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := authn.JWTClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, "subject: %v", claims["sub"])
	})
	_ = h
}

// Verify tokens against an OIDC provider's rotating JWKS (jwks_uri) instead of a
// static key, so signing-key rotation is picked up without a restart.
func ExampleJWTFromKeySource() {
	m := authn.JWTFromKeySource(
		&authn.JWKS{URL: "https://issuer.example.com/.well-known/jwks.json"},
		authn.RS256, // pin the accepted algorithm(s)
	)
	m.Issuer = "https://issuer.example.com"
	m.Audience = "my-api"

	s := parapet.New()
	s.Use(m)
}

// Tune the JWKS cache and accept more than one algorithm.
func ExampleJWKS() {
	jwks := &authn.JWKS{
		URL:                "https://issuer.example.com/.well-known/jwks.json",
		RefreshInterval:    15 * time.Minute, // serve a cached set this long before refreshing
		MinRefreshInterval: time.Minute,      // rate-limit unknown-kid refetches
		MaxResponseBytes:   1 << 20,          // cap the response body at 1 MiB
	}

	s := parapet.New()
	s.Use(authn.JWTFromKeySource(jwks, authn.RS256, authn.ES256))
}

// kidKeys is a custom KeySource backed by a fixed table of public keys keyed by
// the token's "kid". Any KeySource can supply verification keys; the built-in
// JWKS is one implementation.
type kidKeys map[string]any

func (k kidKeys) VerificationKey(_ context.Context, kid string) (any, error) {
	key, ok := k[kid]
	if !ok {
		return nil, fmt.Errorf("unknown kid %q", kid)
	}
	return key, nil
}

// Plug a custom KeySource into the JWT authenticator.
func ExampleKeySource() {
	var current, previous *rsa.PublicKey // during a rotation, accept both

	keys := kidKeys{"2025-current": current, "2025-previous": previous}

	s := parapet.New()
	s.Use(authn.JWTFromKeySource(keys, authn.RS256))
}

// Require HTTP Basic credentials. Comparison is constant-time.
func ExampleBasic() {
	s := parapet.New()
	s.Use(authn.Basic("admin", "s3cret"))
}

// Verify Basic credentials against a custom backend by setting Authenticate
// directly, with a realm reported in the WWW-Authenticate challenge.
func ExampleBasicAuthenticator() {
	m := &authn.BasicAuthenticator{
		Realm: "admin area",
		Authenticate: func(_ *http.Request, username, password string) error {
			// look the user up in a store, verify the password hash, etc.
			if username == "admin" && password == "s3cret" {
				return nil
			}
			return authn.ErrInvalidCredentials
		},
	}

	s := parapet.New()
	s.Use(m)
}

// Delegate the auth decision to an external auth server: the request is allowed
// when the server returns 2xx, and selected response headers are copied onto the
// request for downstream handlers.
func ExampleForward() {
	u, _ := url.Parse("http://auth.internal/verify")

	m := authn.Forward(u)
	m.AuthResponseHeaders = []string{"X-Auth-User", "X-Auth-Roles"}

	s := parapet.New()
	s.Use(m)
}

// Authenticator is the building block the other helpers wrap. Use it directly
// for a custom scheme — here, a static API key. Type is echoed in the
// WWW-Authenticate header on a 401.
func ExampleAuthenticator() {
	m := authn.Authenticator{
		Type: "Bearer",
		Authenticate: func(r *http.Request) error {
			if r.Header.Get("X-API-Key") == "secret-key" {
				return nil
			}
			return authn.ErrMissingAuthorization
		},
	}

	s := parapet.New()
	s.Use(m)
}
