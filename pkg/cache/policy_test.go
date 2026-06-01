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
	return decide(http.MethodGet, 200, h, maxFile, time.Unix(1_000_000, 0)).cacheable
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

func TestDecide_VaryNamedHeaderStillCacheable(t *testing.T) {
	d := decide(http.MethodGet, 200, hdr("Cache-Control", "max-age=60", "Content-Length", "5", "Vary", "Accept-Encoding"), maxFile, time.Now())
	assert.True(t, d.cacheable)
	assert.Equal(t, []string{"accept-encoding"}, d.vary)
}

func TestDecide_StatusCodes(t *testing.T) {
	for _, code := range []int{200, 203, 204, 300, 301, 308, 404, 410} {
		d := decide(http.MethodGet, code, hdr("Cache-Control", "max-age=60", "Content-Length", "1"), maxFile, time.Now())
		assert.True(t, d.cacheable, "status %d should be cacheable with freshness", code)
	}
	for _, code := range []int{201, 302, 401, 403, 500, 502, 206} {
		d := decide(http.MethodGet, code, hdr("Cache-Control", "max-age=60", "Content-Length", "1"), maxFile, time.Now())
		assert.False(t, d.cacheable, "status %d should not be cached", code)
	}
}

func TestDecide_ContentLengthCapAndPresence(t *testing.T) {
	assert.False(t, decide(http.MethodGet, 200, hdr("Cache-Control", "max-age=60"), maxFile, time.Now()).cacheable)
	assert.False(t, decide(http.MethodGet, 200, hdr("Cache-Control", "max-age=60", "Content-Length", "9000000"), maxFile, time.Now()).cacheable)
	assert.True(t, decide(http.MethodHead, 200, hdr("Cache-Control", "max-age=60"), maxFile, time.Now()).cacheable)
}

func TestDecide_Expires(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).UTC().Format(http.TimeFormat)
	past := now.Add(-time.Hour).UTC().Format(http.TimeFormat)
	assert.True(t, decide(http.MethodGet, 200, hdr("Expires", future, "Content-Length", "1"), maxFile, now).cacheable)
	assert.False(t, decide(http.MethodGet, 200, hdr("Expires", past, "Content-Length", "1"), maxFile, now).cacheable)
	assert.False(t, decide(http.MethodGet, 200, hdr("Expires", "0", "Content-Length", "1"), maxFile, now).cacheable)
}

func TestDecide_FarFutureTTLClamped(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	d := decide(http.MethodGet, 200, hdr("Cache-Control", "max-age=7445000000", "Content-Length", "1"), maxFile, now) // ~236y
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
