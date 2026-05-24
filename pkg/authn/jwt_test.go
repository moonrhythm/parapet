package authn_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/moonrhythm/parapet/pkg/authn"
	"github.com/moonrhythm/parapet/pkg/header"
)

var (
	jwtSecret      = []byte("0123456789abcdef0123456789abcdef")
	jwtOtherSecret = []byte("ffffffffffffffffffffffffffffffff")
	jwtNow         = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

func signToken(t *testing.T, alg jose.SignatureAlgorithm, key any, claims any) string {
	t.Helper()
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: alg, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	require.NoError(t, err)
	raw, err := jwt.Signed(sig).Claims(claims).Serialize()
	require.NoError(t, err)
	return raw
}

// serveJWT runs m over a request carrying token (omitted when empty) and
// returns the recorder plus the request seen by the protected handler (nil when
// the handler was not reached).
func serveJWT(m *JWTAuthenticator, token string) (*httptest.ResponseRecorder, *http.Request) {
	var got *http.Request
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest("GET", "/", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w, got
}

func TestJWT(t *testing.T) {
	t.Parallel()

	fixedNow := func() time.Time { return jwtNow }

	t.Run("Valid", func(t *testing.T) {
		token := signToken(t, jose.HS256, jwtSecret, jwt.Claims{
			Subject: "user1",
			Expiry:  jwt.NewNumericDate(jwtNow.Add(time.Hour)),
		})
		m := JWT(jwtSecret, jose.HS256)
		m.Now = fixedNow

		w, got := serveJWT(m, token)
		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, got)
		claims, ok := JWTClaimsFromContext(got.Context())
		assert.True(t, ok)
		assert.Equal(t, "user1", claims["sub"])
	})

	t.Run("MissingAuthorization", func(t *testing.T) {
		m := JWT(jwtSecret, jose.HS256)
		m.Realm = "api"
		w, got := serveJWT(m, "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Nil(t, got)
		assert.Equal(t, `Bearer realm="api"`, w.Header().Get("WWW-Authenticate"))
	})

	t.Run("NotBearer", func(t *testing.T) {
		m := JWT(jwtSecret, jose.HS256)
		h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		}))
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("NoAlgorithmsFailsClosed", func(t *testing.T) {
		token := signToken(t, jose.HS256, jwtSecret, jwt.Claims{Subject: "user1"})
		m := JWT(jwtSecret) // no algorithms pinned
		m.Now = fixedNow
		w, got := serveJWT(m, token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Nil(t, got)
	})

	t.Run("WrongAlgorithmRejected", func(t *testing.T) {
		token := signToken(t, jose.HS256, jwtSecret, jwt.Claims{Subject: "user1"})
		m := JWT(jwtSecret, jose.HS384) // token is HS256
		m.Now = fixedNow
		w, _ := serveJWT(m, token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("BadSignatureRejected", func(t *testing.T) {
		token := signToken(t, jose.HS256, jwtSecret, jwt.Claims{Subject: "user1"})
		m := JWT(jwtOtherSecret, jose.HS256) // verifies with the wrong key
		m.Now = fixedNow
		w, _ := serveJWT(m, token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("ExpiredRejected", func(t *testing.T) {
		token := signToken(t, jose.HS256, jwtSecret, jwt.Claims{
			Subject: "user1",
			Expiry:  jwt.NewNumericDate(jwtNow.Add(-time.Hour)),
		})
		m := JWT(jwtSecret, jose.HS256)
		m.Now = fixedNow
		w, _ := serveJWT(m, token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("NotYetValidRejected", func(t *testing.T) {
		token := signToken(t, jose.HS256, jwtSecret, jwt.Claims{
			Subject:   "user1",
			NotBefore: jwt.NewNumericDate(jwtNow.Add(time.Hour)),
		})
		m := JWT(jwtSecret, jose.HS256)
		m.Now = fixedNow
		w, _ := serveJWT(m, token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Issuer", func(t *testing.T) {
		token := signToken(t, jose.HS256, jwtSecret, jwt.Claims{Issuer: "good"})
		m := JWT(jwtSecret, jose.HS256)
		m.Now = fixedNow
		m.Issuer = "good"
		w, _ := serveJWT(m, token)
		assert.Equal(t, http.StatusOK, w.Code)

		m.Issuer = "other"
		w, _ = serveJWT(m, token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("Audience", func(t *testing.T) {
		token := signToken(t, jose.HS256, jwtSecret, jwt.Claims{Audience: jwt.Audience{"svc"}})
		m := JWT(jwtSecret, jose.HS256)
		m.Now = fixedNow
		m.Audience = "svc"
		w, _ := serveJWT(m, token)
		assert.Equal(t, http.StatusOK, w.Code)

		m.Audience = "other"
		w, _ = serveJWT(m, token)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("ShareValueSlice", func(t *testing.T) {
		m := JWT(jwtSecret, jose.HS256)
		m.Realm = "api"
		m.ShareValueSlice = true
		h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Fail(t, "must not be called")
		}))

		serve := func() []string {
			r := httptest.NewRequest("GET", "/", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			return w.Header()[header.WWWAuthenticate]
		}

		v1 := serve()
		v2 := serve()
		require.NotEmpty(t, v1)
		assert.Equal(t, `Bearer realm="api"`, v1[0])
		assert.Same(t, &v1[0], &v2[0])
	})

	t.Run("NoClaimsInContextWithout", func(t *testing.T) {
		_, ok := JWTClaimsFromContext(httptest.NewRequest("GET", "/", nil).Context())
		assert.False(t, ok)
	})
}
