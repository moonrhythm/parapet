package authn

import jose "github.com/go-jose/go-jose/v4"

// SignatureAlgorithm is a JWS signature (or MAC) algorithm accepted by
// JWTAuthenticator. The values are the standard JWA names from RFC 7518; pin
// the exact algorithm(s) your tokens are signed with so a token signed with any
// other algorithm — including "none" — is rejected.
//
// Use these constants instead of importing a JOSE library directly: the common
// path needs only this package.
type SignatureAlgorithm string

// Supported signature algorithms.
const (
	HS256 SignatureAlgorithm = "HS256" // HMAC using SHA-256
	HS384 SignatureAlgorithm = "HS384" // HMAC using SHA-384
	HS512 SignatureAlgorithm = "HS512" // HMAC using SHA-512
	RS256 SignatureAlgorithm = "RS256" // RSASSA-PKCS#1 v1.5 using SHA-256
	RS384 SignatureAlgorithm = "RS384" // RSASSA-PKCS#1 v1.5 using SHA-384
	RS512 SignatureAlgorithm = "RS512" // RSASSA-PKCS#1 v1.5 using SHA-512
	ES256 SignatureAlgorithm = "ES256" // ECDSA using P-256 and SHA-256
	ES384 SignatureAlgorithm = "ES384" // ECDSA using P-384 and SHA-384
	ES512 SignatureAlgorithm = "ES512" // ECDSA using P-521 and SHA-512
	PS256 SignatureAlgorithm = "PS256" // RSASSA-PSS using SHA-256 and MGF1 with SHA-256
	PS384 SignatureAlgorithm = "PS384" // RSASSA-PSS using SHA-384 and MGF1 with SHA-384
	PS512 SignatureAlgorithm = "PS512" // RSASSA-PSS using SHA-512 and MGF1 with SHA-512
	EdDSA SignatureAlgorithm = "EdDSA" // EdDSA using Ed25519
)

// toJOSEAlgorithms converts the public algorithm allowlist to the underlying
// JOSE type. The JWA string values are identical, so this is a plain remap that
// keeps the third-party type off the package's public API.
func toJOSEAlgorithms(algs []SignatureAlgorithm) []jose.SignatureAlgorithm {
	if len(algs) == 0 {
		return nil
	}
	out := make([]jose.SignatureAlgorithm, len(algs))
	for i, a := range algs {
		out[i] = jose.SignatureAlgorithm(a)
	}
	return out
}
