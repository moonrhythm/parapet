package cache

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const maxFile = 8 << 20

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func cacheableGET(t *testing.T, h http.Header) bool {
	t.Helper()
	if h.Get("Content-Length") == "" {
		h.Set("Content-Length", "10")
	}
	return decide(http.MethodGet, 200, h, false, maxFile, time.Unix(1_000_000, 0)).cacheable
}

func TestDecide_HonorsOriginFreshnessOnly(t *testing.T) {
	assert.True(t, cacheableGET(t, hdr("Cache-Control", "public, max-age=60")))
	assert.True(t, cacheableGET(t, hdr("Cache-Control", "max-age=60")))
	assert.True(t, cacheableGET(t, hdr("Cache-Control", "s-maxage=30")))
	assert.False(t, cacheableGET(t, hdr()))
	assert.False(t, cacheableGET(t, hdr("Content-Type", "text/html")))
}

func TestDecide_RefusesPrivateNoStoreNoCacheSetCookieVaryStar(t *testing.T) {
	assert.False(t, cacheableGET(t, hdr("Cache-Control", "private, max-age=60")))
	assert.False(t, cacheableGET(t, hdr("Cache-Control", "no-store")))
	assert.False(t, cacheableGET(t, hdr("Cache-Control", "no-cache, max-age=60")))
	assert.False(t, cacheableGET(t, hdr("Cache-Control", "public, max-age=60", "Set-Cookie", "sid=abc")))
	assert.False(t, cacheableGET(t, hdr("Cache-Control", "public, max-age=60", "Vary", "*")))
}

// An Authorization-bearing request's response is only shared-cacheable when the
// origin explicitly opts in via public, s-maxage, or must-revalidate (RFC 9111
// §3.5). A bare max-age is refused so it can't be served to other users.
func TestDecide_AuthorizationRequiresExplicitSharedOptIn(t *testing.T) {
	cl := func(cc string) http.Header { return hdr("Cache-Control", cc, "Content-Length", "1") }
	now := time.Now()

	// Authorized request: bare max-age (or Expires) is NOT shared-cacheable.
	assert.False(t, decide(http.MethodGet, 200, cl("max-age=60"), true, maxFile, now).cacheable)
	assert.False(t, decide(http.MethodGet, 200, hdr("Expires", now.Add(time.Hour).UTC().Format(http.TimeFormat), "Content-Length", "1"), true, maxFile, now).cacheable)

	// Explicit shared opt-in re-enables caching for an authorized request.
	assert.True(t, decide(http.MethodGet, 200, cl("public, max-age=60"), true, maxFile, now).cacheable)
	assert.True(t, decide(http.MethodGet, 200, cl("s-maxage=60"), true, maxFile, now).cacheable)
	assert.True(t, decide(http.MethodGet, 200, cl("must-revalidate, max-age=60"), true, maxFile, now).cacheable)

	// An unauthenticated request is unaffected: bare max-age still caches.
	assert.True(t, decide(http.MethodGet, 200, cl("max-age=60"), false, maxFile, now).cacheable)
}

func TestDecide_VaryNamedHeaderStillCacheable(t *testing.T) {
	d := decide(http.MethodGet, 200, hdr("Cache-Control", "max-age=60", "Content-Length", "5", "Vary", "Accept-Encoding"), false, maxFile, time.Now())
	assert.True(t, d.cacheable)
	assert.Equal(t, []string{"accept-encoding"}, d.vary)
}

func TestDecide_StatusCodes(t *testing.T) {
	for _, code := range []int{200, 203, 204, 300, 301, 308, 404, 410} {
		d := decide(http.MethodGet, code, hdr("Cache-Control", "max-age=60", "Content-Length", "1"), false, maxFile, time.Now())
		assert.True(t, d.cacheable, "status %d should be cacheable with freshness", code)
	}
	for _, code := range []int{201, 302, 401, 403, 500, 502, 206} {
		d := decide(http.MethodGet, code, hdr("Cache-Control", "max-age=60", "Content-Length", "1"), false, maxFile, time.Now())
		assert.False(t, d.cacheable, "status %d should not be cached", code)
	}
}

func TestDecide_ContentLengthCapAndPresence(t *testing.T) {
	assert.False(t, decide(http.MethodGet, 200, hdr("Cache-Control", "max-age=60"), false, maxFile, time.Now()).cacheable)
	assert.False(t, decide(http.MethodGet, 200, hdr("Cache-Control", "max-age=60", "Content-Length", "9000000"), false, maxFile, time.Now()).cacheable)
	assert.True(t, decide(http.MethodHead, 200, hdr("Cache-Control", "max-age=60"), false, maxFile, time.Now()).cacheable)
}

func TestDecide_Expires(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).UTC().Format(http.TimeFormat)
	past := now.Add(-time.Hour).UTC().Format(http.TimeFormat)
	assert.True(t, decide(http.MethodGet, 200, hdr("Expires", future, "Content-Length", "1"), false, maxFile, now).cacheable)
	assert.False(t, decide(http.MethodGet, 200, hdr("Expires", past, "Content-Length", "1"), false, maxFile, now).cacheable)
	assert.False(t, decide(http.MethodGet, 200, hdr("Expires", "0", "Content-Length", "1"), false, maxFile, now).cacheable)
}

func TestDecide_FarFutureTTLClamped(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	d := decide(http.MethodGet, 200, hdr("Cache-Control", "max-age=7445000000", "Content-Length", "1"), false, maxFile, now) // ~236y
	assert.True(t, d.cacheable)
	// clamped well within UnixNano range (year < 2262)
	assert.True(t, d.freshUntil.Before(time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)))
	assert.False(t, d.freshUntil.IsZero())
}

func TestParseVary(t *testing.T) {
	names, star := parseVary(hdr("Vary", "Accept-Encoding, Accept", "Vary", "accept-encoding"))
	assert.False(t, star)
	assert.ElementsMatch(t, []string{"accept-encoding", "accept"}, names)
	_, star = parseVary(hdr("Vary", "Accept, *"))
	assert.True(t, star)
}

func TestFreshness_SMaxAgeWins(t *testing.T) {
	cc := parseCacheControl(hdr("Cache-Control", "max-age=10, s-maxage=99"))
	assert.Equal(t, 99*time.Second, freshness(cc, http.Header{}, time.Now()))
}

// The remaining lifetime is reduced by the response's age (RFC 9111 §4.2.3): the
// larger of the Age header and the apparent age (now - Date).
func TestFreshness_SubtractsResponseAge(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cc := parseCacheControl(hdr("Cache-Control", "max-age=60"))
	date50 := now.Add(-50 * time.Second).UTC().Format(http.TimeFormat)

	assert.Equal(t, 60*time.Second, freshness(cc, http.Header{}, now), "no Age/Date: full lifetime")
	assert.Equal(t, 5*time.Second, freshness(cc, hdr("Age", "55"), now), "Age header subtracted")
	assert.Equal(t, 10*time.Second, freshness(cc, hdr("Date", date50), now), "apparent age (now-Date) subtracted")
	assert.Equal(t, 5*time.Second, freshness(cc, hdr("Age", "55", "Date", date50), now), "larger of Age and apparent age wins")
	assert.LessOrEqual(t, freshness(cc, hdr("Age", "120"), now), time.Duration(0), "age beyond lifetime: not fresh")
}

// An already-aged response stays cacheable only for its remaining lifetime, and a
// response aged past its lifetime is not cached at all.
func TestDecide_AgeReducesFreshness(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	d := decide(http.MethodGet, 200, hdr("Cache-Control", "max-age=60", "Age", "55", "Content-Length", "1"), maxFile, now)
	assert.True(t, d.cacheable)
	assert.Equal(t, now.Add(5*time.Second), d.freshUntil, "freshUntil reflects the ~5s remaining after Age")

	assert.False(t, decide(http.MethodGet, 200, hdr("Cache-Control", "max-age=60", "Age", "60", "Content-Length", "1"), maxFile, now).cacheable,
		"Age >= max-age: already stale, not cached")
}
