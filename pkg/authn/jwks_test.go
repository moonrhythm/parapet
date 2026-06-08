package authn_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/moonrhythm/parapet/pkg/authn"
)

// testClock is a manually-advanced clock for exercising JWKS cache aging.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// jwksServer is a swappable, hit-counting jwks_uri for tests.
type jwksServer struct {
	mu     sync.Mutex
	keys   []jose.JSONWebKey
	status int // 0 => 200
	hits   int
}

func (s *jwksServer) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.hits++
	status := s.status
	set := jose.JSONWebKeySet{Keys: append([]jose.JSONWebKey(nil), s.keys...)}
	s.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(set)
}

func (s *jwksServer) setKeys(keys ...jose.JSONWebKey) {
	s.mu.Lock()
	s.keys = keys
	s.mu.Unlock()
}

func (s *jwksServer) setStatus(status int) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

func (s *jwksServer) hitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

func pubJWK(key *rsa.PrivateKey, kid string) jose.JSONWebKey {
	return jose.JSONWebKey{Key: key.Public(), KeyID: kid, Algorithm: string(jose.RS256), Use: "sig"}
}

// signRSA signs claims with key under RS256, embedding kid in the JOSE header.
func signRSA(t *testing.T, key *rsa.PrivateKey, kid string, claims any) string {
	t.Helper()
	jwk := jose.JSONWebKey{Key: key, KeyID: kid, Algorithm: string(jose.RS256)}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jwk}, (&jose.SignerOptions{}).WithType("JWT"))
	require.NoError(t, err)
	raw, err := jwt.Signed(sig).Claims(claims).Serialize()
	require.NoError(t, err)
	return raw
}

func validClaims() jwt.Claims {
	return jwt.Claims{Subject: "user1", Expiry: jwt.NewNumericDate(jwtNow.Add(time.Hour))}
}

func fixedNow() time.Time { return jwtNow }

func TestJWKS(t *testing.T) {
	t.Parallel()

	k1, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	k2, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	newServer := func(keys ...jose.JSONWebKey) (*jwksServer, *httptest.Server) {
		srv := &jwksServer{keys: keys}
		ts := httptest.NewServer(srv)
		return srv, ts
	}

	t.Run("ValidAndCached", func(t *testing.T) {
		srv, ts := newServer(pubJWK(k1, "k1"))
		defer ts.Close()

		m := JWTFromKeySource(&JWKS{URL: ts.URL}, jose.RS256)
		m.Now = fixedNow
		token := signRSA(t, k1, "k1", validClaims())

		w, got := serveJWT(m, token)
		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, got)
		claims, ok := JWTClaimsFromContext(got.Context())
		assert.True(t, ok)
		assert.Equal(t, "user1", claims["sub"])
		assert.Equal(t, 1, srv.hitCount())

		// A second request within the refresh interval is served from cache.
		w, _ = serveJWT(m, token)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 1, srv.hitCount(), "second verification must not refetch")
	})

	t.Run("RotationViaUnknownKid", func(t *testing.T) {
		srv, ts := newServer(pubJWK(k1, "k1"))
		defer ts.Close()

		clock := &testClock{t: jwtNow}
		m := JWTFromKeySource(&JWKS{URL: ts.URL, Now: clock.now, MinRefreshInterval: time.Minute}, jose.RS256)
		m.Now = fixedNow

		// Prime the cache with the k1 set.
		w, _ := serveJWT(m, signRSA(t, k1, "k1", validClaims()))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, 1, srv.hitCount())

		// Origin rotates to k2; past the rate-limit window an unknown kid forces
		// a blocking refetch that then verifies the new token.
		srv.setKeys(pubJWK(k2, "k2"))
		clock.advance(2 * time.Minute)

		w, _ = serveJWT(m, signRSA(t, k2, "k2", validClaims()))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 2, srv.hitCount())
	})

	t.Run("UnknownKidRateLimited", func(t *testing.T) {
		srv, ts := newServer(pubJWK(k1, "k1"))
		defer ts.Close()

		clock := &testClock{t: jwtNow}
		m := JWTFromKeySource(&JWKS{URL: ts.URL, Now: clock.now, MinRefreshInterval: time.Hour}, jose.RS256)
		m.Now = fixedNow

		w, _ := serveJWT(m, signRSA(t, k1, "k1", validClaims()))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, 1, srv.hitCount())

		// Unknown kid within the rate-limit window: no refetch, token rejected.
		w, got := serveJWT(m, signRSA(t, k2, "k2", validClaims()))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Nil(t, got)
		assert.Equal(t, 1, srv.hitCount(), "bogus kid must not hammer the jwks_uri")
	})

	t.Run("StaleServedThenBackgroundRefresh", func(t *testing.T) {
		srv, ts := newServer(pubJWK(k1, "k1"))
		defer ts.Close()

		clock := &testClock{t: jwtNow}
		m := JWTFromKeySource(&JWKS{URL: ts.URL, Now: clock.now, RefreshInterval: 15 * time.Minute}, jose.RS256)
		m.Now = fixedNow

		w, _ := serveJWT(m, signRSA(t, k1, "k1", validClaims()))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, 1, srv.hitCount())

		// Origin now publishes both keys; advance past the TTL.
		srv.setKeys(pubJWK(k1, "k1"), pubJWK(k2, "k2"))
		clock.advance(16 * time.Minute)

		// A known-kid token is served immediately from the stale set...
		w, _ = serveJWT(m, signRSA(t, k1, "k1", validClaims()))
		assert.Equal(t, http.StatusOK, w.Code)

		// ...while a background refetch picks up the rotated set.
		require.Eventually(t, func() bool { return srv.hitCount() == 2 }, 2*time.Second, 5*time.Millisecond)

		// The newly-published k2 now verifies without another fetch.
		w, _ = serveJWT(m, signRSA(t, k2, "k2", validClaims()))
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 2, srv.hitCount())
	})

	t.Run("FailStaticKeepsLastGoodSet", func(t *testing.T) {
		srv, ts := newServer(pubJWK(k1, "k1"))
		defer ts.Close()

		clock := &testClock{t: jwtNow}
		m := JWTFromKeySource(&JWKS{URL: ts.URL, Now: clock.now, RefreshInterval: 15 * time.Minute}, jose.RS256)
		m.Now = fixedNow

		w, _ := serveJWT(m, signRSA(t, k1, "k1", validClaims()))
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, 1, srv.hitCount())

		// Origin starts failing; the stale-triggered background refetch fails.
		srv.setStatus(http.StatusInternalServerError)
		clock.advance(16 * time.Minute)

		w, _ = serveJWT(m, signRSA(t, k1, "k1", validClaims()))
		assert.Equal(t, http.StatusOK, w.Code, "a failed refresh must keep the last good key set")
		require.Eventually(t, func() bool { return srv.hitCount() >= 2 }, 2*time.Second, 5*time.Millisecond)

		// Still serving the cached k1 key on subsequent requests.
		w, _ = serveJWT(m, signRSA(t, k1, "k1", validClaims()))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("FirstFetchFailureRejects", func(t *testing.T) {
		srv, ts := newServer(pubJWK(k1, "k1"))
		defer ts.Close()
		srv.setStatus(http.StatusInternalServerError)

		m := JWTFromKeySource(&JWKS{URL: ts.URL}, jose.RS256)
		m.Now = fixedNow

		w, got := serveJWT(m, signRSA(t, k1, "k1", validClaims()))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Nil(t, got)
	})

	t.Run("VerificationKeyReportsNoKeysOnFirstFailure", func(t *testing.T) {
		srv, ts := newServer(pubJWK(k1, "k1"))
		defer ts.Close()
		srv.setStatus(http.StatusInternalServerError)

		jwks := &JWKS{URL: ts.URL}
		_, err := jwks.VerificationKey(context.Background(), "k1")
		assert.ErrorIs(t, err, ErrNoKeys)
	})

	t.Run("WrongSigningKeyRejected", func(t *testing.T) {
		_, ts := newServer(pubJWK(k1, "k1"))
		defer ts.Close()

		m := JWTFromKeySource(&JWKS{URL: ts.URL}, jose.RS256)
		m.Now = fixedNow

		// kid claims k1 but the token is actually signed by k2.
		w, got := serveJWT(m, signRSA(t, k2, "k1", validClaims()))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Nil(t, got)
	})

	t.Run("WrongAlgorithmRejected", func(t *testing.T) {
		_, ts := newServer(pubJWK(k1, "k1"))
		defer ts.Close()

		// Pin ES256 while the token is RS256: rejected before key lookup.
		m := JWTFromKeySource(&JWKS{URL: ts.URL}, jose.ES256)
		m.Now = fixedNow

		w, _ := serveJWT(m, signRSA(t, k1, "k1", validClaims()))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("NoAlgorithmsFailsClosed", func(t *testing.T) {
		srv, ts := newServer(pubJWK(k1, "k1"))
		defer ts.Close()

		m := JWTFromKeySource(&JWKS{URL: ts.URL}, jose.RS256) // pinned alg
		m.Algorithms = nil                                    // ...then cleared
		m.Now = fixedNow

		w, got := serveJWT(m, signRSA(t, k1, "k1", validClaims()))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Nil(t, got)
		assert.Equal(t, 0, srv.hitCount(), "must reject before fetching keys")
	})
}
