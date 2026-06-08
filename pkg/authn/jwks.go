package authn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// KeySource supplies the verification key for JWTAuthenticator at request time,
// which lets the signing key rotate without restarting the process. kid is the
// "kid" from the token's JOSE header ("" when the token carries none); an
// implementation may use it to decide whether a refresh is warranted.
//
// The returned value must be one of the types go-jose accepts for verification
// (see JWT). Returning a *jose.JSONWebKeySet is the common case: go-jose then
// selects the key whose "kid" matches the token header and verifies with the
// algorithm the JWTAuthenticator pinned.
type KeySource interface {
	VerificationKey(ctx context.Context, kid string) (any, error)
}

// ErrNoKeys is returned by a KeySource that has never successfully obtained any
// key material (e.g. a JWKS whose first fetch failed). Once any key set has been
// fetched, a later refresh failure is absorbed (fail-static) rather than
// surfaced.
var ErrNoKeys = errors.New("authn: no verification keys")

var defaultJWKSClient = &http.Client{Timeout: 10 * time.Second}

const (
	defaultJWKSRefreshInterval    = 15 * time.Minute
	defaultJWKSMinRefreshInterval = time.Minute
	defaultJWKSMaxResponseBytes   = 1 << 20 // 1 MiB
)

// JWKS is a KeySource backed by a remote JSON Web Key Set — an OIDC provider's
// jwks_uri (e.g. Auth0, Okta, Google). It fetches the set over HTTP and caches
// it, refetching when the cache goes stale or when a token presents a kid the
// cache doesn't know, so signing-key rotation is picked up without a restart.
//
// Fetches are single-flighted: concurrent verifications that need a refresh
// share one HTTP request. It is fail-static — once a key set has been fetched,
// a later refresh failure keeps the last good set in service instead of
// rejecting requests. A merely-stale refresh happens in the background and
// serves the cached set meanwhile; only an empty cache or an unknown kid blocks
// the caller on the fetch.
//
// JWKS is safe for concurrent use. The zero value is not usable: set URL (and
// optionally the tuning fields) and pass it as JWTAuthenticator.KeySource.
//
//nolint:govet
type JWKS struct {
	// URL is the jwks_uri to fetch the key set from. Required.
	URL string

	// Client fetches the key set. Defaults to an *http.Client with a 10s
	// timeout. Give it a tighter timeout or a custom transport as needed.
	Client *http.Client

	// RefreshInterval is how long a fetched set is served before it is treated
	// as stale and refetched (in the background) on the next use. Defaults to
	// 15m.
	RefreshInterval time.Duration

	// MinRefreshInterval rate-limits unknown-kid refetches: a token whose kid is
	// absent from the cached set triggers a refetch only if at least this long
	// has passed since the last fetch. It stops a flood of tokens bearing bogus
	// kids from hammering the jwks_uri. Defaults to 1m.
	MinRefreshInterval time.Duration

	// MaxResponseBytes caps how many bytes of the JWKS response body are read,
	// defending against a hostile or runaway endpoint. Defaults to 1 MiB.
	MaxResponseBytes int64

	// Now overrides the clock used for cache aging; mainly for tests. Defaults
	// to time.Now.
	Now func() time.Time

	mu        sync.Mutex
	set       *jose.JSONWebKeySet
	fetchedAt time.Time
	inflight  *fetchCall
}

// fetchCall is one in-flight JWKS fetch that concurrent callers wait on.
type fetchCall struct {
	done chan struct{}
	set  *jose.JSONWebKeySet
	err  error
}

// VerificationKey implements KeySource. It returns the cached key set,
// refreshing it as needed (see JWKS).
func (j *JWKS) VerificationKey(ctx context.Context, kid string) (any, error) {
	j.mu.Lock()
	set, fetchedAt := j.set, j.fetchedAt
	age := j.now().Sub(fetchedAt)
	j.mu.Unlock()

	fresh := set != nil && age < j.refreshInterval()
	known := set != nil && (kid == "" || containsKID(set, kid))

	switch {
	case set != nil && fresh && known:
		// Fast path: usable set in hand.
		return set, nil

	case set != nil && known && !fresh:
		// Stale but usable: serve it now, refresh in the background.
		j.startFetch()
		return set, nil

	case set != nil && !known && age < j.minRefreshInterval():
		// Unknown kid, but we refetched too recently to try again. Serve what we
		// have; go-jose will reject the token as kid-not-found.
		return set, nil
	}

	// Blocking fetch: the cache is empty, or a possibly-rotated kid is unknown
	// and we are past the rate-limit window.
	c := j.startFetch()
	select {
	case <-ctx.Done():
		if set != nil {
			return set, nil // caller gave up; fall back to the stale set
		}
		return nil, ctx.Err()
	case <-c.done:
	}

	if c.err != nil {
		if set != nil {
			return set, nil // fail-static: keep the last good set
		}
		return nil, fmt.Errorf("%w: %w", ErrNoKeys, c.err)
	}
	return c.set, nil
}

// startFetch returns the in-flight fetch, starting one if none is running. The
// fetch runs on a detached context so it isn't cancelled when the request that
// triggered it ends; it is bounded by the Client timeout. Caller must not hold
// j.mu.
func (j *JWKS) startFetch() *fetchCall {
	j.mu.Lock()
	if j.inflight != nil {
		c := j.inflight
		j.mu.Unlock()
		return c
	}
	c := &fetchCall{done: make(chan struct{})}
	j.inflight = c
	j.mu.Unlock()

	go func() {
		set, err := j.fetch(context.Background())

		j.mu.Lock()
		if err == nil {
			j.set = set
			j.fetchedAt = j.now()
		}
		j.inflight = nil
		j.mu.Unlock()

		c.set, c.err = set, err
		close(c.done)
	}()
	return c
}

// fetch retrieves and decodes the key set from URL.
func (j *JWKS) fetch(ctx context.Context) (*jose.JSONWebKeySet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := j.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("authn: jwks fetch: unexpected status %d", resp.StatusCode)
	}

	var set jose.JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(resp.Body, j.maxResponseBytes())).Decode(&set); err != nil {
		return nil, fmt.Errorf("authn: jwks decode: %w", err)
	}
	if len(set.Keys) == 0 {
		return nil, errors.New("authn: jwks: empty key set")
	}
	return &set, nil
}

func (j *JWKS) client() *http.Client {
	if j.Client != nil {
		return j.Client
	}
	return defaultJWKSClient
}

func (j *JWKS) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

func (j *JWKS) refreshInterval() time.Duration {
	if j.RefreshInterval > 0 {
		return j.RefreshInterval
	}
	return defaultJWKSRefreshInterval
}

func (j *JWKS) minRefreshInterval() time.Duration {
	if j.MinRefreshInterval > 0 {
		return j.MinRefreshInterval
	}
	return defaultJWKSMinRefreshInterval
}

func (j *JWKS) maxResponseBytes() int64 {
	if j.MaxResponseBytes > 0 {
		return j.MaxResponseBytes
	}
	return defaultJWKSMaxResponseBytes
}

// containsKID reports whether set has a key with the given kid.
func containsKID(set *jose.JSONWebKeySet, kid string) bool {
	return len(set.Key(kid)) > 0
}
