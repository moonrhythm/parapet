package authn

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/moonrhythm/parapet/pkg/header"
)

// ErrInvalidToken is returned when a bearer token is missing required
// properties, fails signature verification, or fails claim validation.
var ErrInvalidToken = errors.New("invalid token")

// JWT creates a new JWT bearer-token authentication middleware. It reads the
// token from the Authorization: Bearer header, verifies its signature against
// key, and accepts only the listed signature algorithms.
//
// The algorithm allowlist is mandatory: a token signed with any other
// algorithm — including "none" — is rejected. Pinning the algorithms this way
// prevents algorithm-confusion attacks, where an attacker re-signs a token with
// an algorithm the verifier did not intend to accept.
//
// algs pins the accepted algorithms with this package's SignatureAlgorithm
// constants (e.g. authn.RS256) — callers don't import a JOSE library.
//
// key is a standard crypto key: []byte for HMAC (HS256/384/512), or an
// *rsa.PublicKey, *ecdsa.PublicKey or ed25519.PublicKey for asymmetric
// signatures.
//
// To verify against a rotating remote key set instead of a static key, leave
// key nil and set JWTAuthenticator.KeySource (see JWTFromKeySource and JWKS).
func JWT(key any, algs ...SignatureAlgorithm) *JWTAuthenticator {
	return &JWTAuthenticator{
		Key:        key,
		Algorithms: algs,
	}
}

// JWTFromKeySource creates a JWT bearer-token authentication middleware that
// resolves its verification key from src at request time — typically a remote,
// rotating JWKS via JWKS — instead of a fixed key. The algorithm allowlist is
// still mandatory and enforced exactly as in JWT.
func JWTFromKeySource(src KeySource, algs ...SignatureAlgorithm) *JWTAuthenticator {
	return &JWTAuthenticator{
		KeySource:  src,
		Algorithms: algs,
	}
}

// JWTAuthenticator middleware
//
//nolint:govet
type JWTAuthenticator struct {
	// Key verifies the token signature. See JWT for accepted types. Ignored when
	// KeySource is set.
	Key any

	// KeySource, when set, supplies the verification key dynamically at request
	// time (e.g. a rotating remote JWKS via JWKS) and takes precedence over Key.
	// The token's "kid" header is passed to it so it can select or refresh keys.
	KeySource KeySource

	// Algorithms is the set of accepted signature algorithms. It is required;
	// when empty every request is rejected.
	Algorithms []SignatureAlgorithm

	// Issuer and Audience, when set, must match the token's "iss" and "aud"
	// claims respectively.
	Issuer   string
	Audience string

	// Leeway tolerates clock skew when checking the time-based claims "exp",
	// "nbf" and "iat". Defaults to jwt.DefaultLeeway (1 minute).
	Leeway time.Duration

	// Realm is reported in the WWW-Authenticate challenge on rejection.
	Realm string

	// Now overrides the clock used for claim validation. Defaults to time.Now;
	// mainly useful for tests.
	Now func() time.Time

	// ShareValueSlice shares the fixed WWW-Authenticate value slice across
	// rejected responses instead of allocating one per request. See
	// Authenticator.ShareValueSlice.
	ShareValueSlice bool
}

// ServeHandler implements middleware interface
func (m JWTAuthenticator) ServeHandler(h http.Handler) http.Handler {
	challenge := "Bearer"
	if m.Realm != "" {
		challenge += ` realm="` + url.PathEscape(m.Realm) + `"`
	}

	leeway := m.Leeway
	if leeway == 0 {
		leeway = jwt.DefaultLeeway
	}

	now := m.Now
	if now == nil {
		now = time.Now
	}

	// Convert the pinned algorithms to the JOSE type once, at setup.
	algs := toJOSEAlgorithms(m.Algorithms)

	return Authenticator{
		Type:            challenge,
		ShareValueSlice: m.ShareValueSlice,
		Authenticate: func(r *http.Request) error {
			raw, ok := bearerToken(r)
			if !ok {
				return ErrMissingAuthorization
			}

			// Fail closed when no algorithm is pinned, rather than trusting
			// whatever the token header claims.
			if len(algs) == 0 {
				return ErrInvalidToken
			}

			tok, err := jwt.ParseSigned(raw, algs)
			if err != nil {
				return ErrInvalidToken
			}

			// Resolve the verification key. A KeySource (e.g. a rotating remote
			// JWKS) takes precedence over the static Key and is handed the
			// token's kid so it can select or refresh the right key.
			key := m.Key
			if m.KeySource != nil {
				var kid string
				if len(tok.Headers) > 0 {
					kid = tok.Headers[0].KeyID
				}
				key, err = m.KeySource.VerificationKey(r.Context(), kid)
				if err != nil {
					return ErrInvalidToken
				}
			}

			// Claims verifies the signature, then decodes the payload into the
			// registered-claims struct (for validation) and a map (for
			// downstream consumers).
			var claims jwt.Claims
			all := map[string]any{}
			if err := tok.Claims(key, &claims, &all); err != nil {
				return ErrInvalidToken
			}

			expected := jwt.Expected{Time: now()}
			if m.Issuer != "" {
				expected.Issuer = m.Issuer
			}
			if m.Audience != "" {
				expected.AnyAudience = jwt.Audience{m.Audience}
			}
			if err := claims.ValidateWithLeeway(expected, leeway); err != nil {
				return ErrInvalidToken
			}

			// Expose the verified claims to downstream handlers. Authenticator
			// reuses this *http.Request when calling the next handler, so update
			// its context in place.
			*r = *r.WithContext(context.WithValue(r.Context(), jwtClaimsContextKey{}, all))
			return nil
		},
	}.ServeHandler(h)
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. The scheme is matched case-insensitively per RFC 7235.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "bearer "
	v := header.Get(r.Header, header.Authorization)
	if len(v) < len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(v[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

type jwtClaimsContextKey struct{}

// JWTClaimsFromContext returns the verified JWT claims that JWTAuthenticator
// stored on the request context, if the request was authenticated by it.
func JWTClaimsFromContext(ctx context.Context) (map[string]any, bool) {
	c, ok := ctx.Value(jwtClaimsContextKey{}).(map[string]any)
	return c, ok
}
