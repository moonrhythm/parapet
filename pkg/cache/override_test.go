package cache

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecideForced(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	const maxFile = 1 << 20

	hdr := func(pairs ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(pairs); i += 2 {
			h.Add(pairs[i], pairs[i+1])
		}
		if h.Get("Content-Length") == "" {
			h.Set("Content-Length", "3")
		}
		return h
	}

	cases := []struct {
		name          string
		mode          OverrideMode
		status        int
		h             http.Header
		auth          bool
		wantCacheable bool
		wantTTL       time.Duration // 0 = don't check
	}{
		{"balanced forces a bare 200", OverrideBalanced, 200, hdr(), false, true, time.Hour},
		{"balanced overrides no-cache", OverrideBalanced, 200, hdr("Cache-Control", "no-cache"), false, true, time.Hour},
		{"balanced overrides a short max-age", OverrideBalanced, 200, hdr("Cache-Control", "max-age=5"), false, true, time.Hour},
		{"balanced refuses no-store", OverrideBalanced, 200, hdr("Cache-Control", "no-store"), false, false, 0},
		{"balanced refuses private", OverrideBalanced, 200, hdr("Cache-Control", "private"), false, false, 0},
		{"balanced refuses Set-Cookie", OverrideBalanced, 200, hdr("Set-Cookie", "a=b"), false, false, 0},
		{"balanced refuses auth without opt-in", OverrideBalanced, 200, hdr(), true, false, 0},
		{"balanced allows auth with public", OverrideBalanced, 200, hdr("Cache-Control", "public"), true, true, time.Hour},

		{"conservative honors origin freshness", OverrideConservative, 200, hdr("Cache-Control", "max-age=10"), false, true, 10 * time.Second},
		{"conservative fills missing freshness", OverrideConservative, 200, hdr(), false, true, time.Hour},
		{"conservative refuses no-cache", OverrideConservative, 200, hdr("Cache-Control", "no-cache"), false, false, 0},
		{"conservative refuses no-store", OverrideConservative, 200, hdr("Cache-Control", "no-store"), false, false, 0},

		{"aggressive caches no-store", OverrideAggressive, 200, hdr("Cache-Control", "no-store"), false, true, time.Hour},
		{"aggressive caches private", OverrideAggressive, 200, hdr("Cache-Control", "private"), false, true, time.Hour},
		{"aggressive caches an authed request", OverrideAggressive, 200, hdr(), true, true, time.Hour},
		{"aggressive still refuses Set-Cookie", OverrideAggressive, 200, hdr("Set-Cookie", "a=b"), false, false, 0},

		{"any mode refuses a non-cacheable status", OverrideAggressive, 500, hdr(), false, false, 0},
		{"any mode refuses Vary: *", OverrideAggressive, 200, hdr("Vary", "*"), false, false, 0},
		{"any mode refuses oversize", OverrideBalanced, 200, hdr("Content-Length", "9000000"), false, false, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := decideForced(http.MethodGet, tc.status, tc.h, tc.auth, maxFile, now, &Override{TTL: time.Hour, Mode: tc.mode})
			assert.Equal(t, tc.wantCacheable, d.cacheable)
			if tc.wantCacheable && tc.wantTTL > 0 {
				assert.Equal(t, now.Add(tc.wantTTL), d.freshUntil)
			}
		})
	}

	t.Run("forces the RFC 5861 windows", func(t *testing.T) {
		d := decideForced(http.MethodGet, 200, hdr(), false, maxFile, now,
			&Override{TTL: time.Hour, StaleWhileRevalidate: 30 * time.Second, StaleIfError: time.Hour})
		require.True(t, d.cacheable)
		assert.Equal(t, 30*time.Second, d.staleWhileRevalidate)
		assert.Equal(t, time.Hour, d.staleIfError)
		assert.False(t, d.noStale)
	})

	t.Run("clamps an absurd TTL", func(t *testing.T) {
		d := decideForced(http.MethodGet, 200, hdr(), false, maxFile, now, &Override{TTL: 100 * 365 * 24 * time.Hour})
		require.True(t, d.cacheable)
		assert.Equal(t, now.Add(maxTTL), d.freshUntil)
	})

	t.Run("HEAD needs no Content-Length", func(t *testing.T) {
		h := http.Header{} // no Content-Length
		d := decideForced(http.MethodHead, 200, h, false, maxFile, now, &Override{TTL: time.Hour})
		assert.True(t, d.cacheable)
	})

	t.Run("conservative respects must-revalidate over forced windows", func(t *testing.T) {
		d := decideForced(http.MethodGet, 200, hdr("Cache-Control", "must-revalidate, max-age=60"), false, maxFile, now,
			&Override{TTL: time.Hour, StaleWhileRevalidate: 30 * time.Minute, Mode: OverrideConservative})
		require.True(t, d.cacheable)
		assert.True(t, d.noStale)
		assert.Zero(t, d.staleWhileRevalidate, "must-revalidate suppresses the forced window in Conservative mode")
		assert.Equal(t, now.Add(60*time.Second), d.freshUntil, "origin freshness is honored")
	})

	t.Run("balanced forces windows despite must-revalidate", func(t *testing.T) {
		d := decideForced(http.MethodGet, 200, hdr("Cache-Control", "must-revalidate, max-age=60"), false, maxFile, now,
			&Override{TTL: time.Hour, StaleWhileRevalidate: 30 * time.Minute, Mode: OverrideBalanced})
		require.True(t, d.cacheable)
		assert.False(t, d.noStale)
		assert.Equal(t, 30*time.Minute, d.staleWhileRevalidate)
	})
}

// The Authorization gate holds under OverrideBalanced end-to-end: an authed
// request is cached only when the origin opts the response in for sharing.
func TestCache_Override_BalancedAuthGate(t *testing.T) {
	c := New(NewMemory(1<<20), Options{
		MaxFileSize: 1024,
		Override:    func(_ *http.Request, _ int, _ http.Header) *Override { return &Override{TTL: time.Hour} },
	})
	authed := http.Header{"Authorization": {"Bearer x"}}

	// No shared opt-in -> never cached, even though caching is forced.
	secret := origin(originSpec{body: []byte("secret")}, new(int32))
	assert.Equal(t, "MISS", do(c, secret, "GET", "/x", authed).Header().Get("X-Cache"))
	assert.Equal(t, "MISS", do(c, secret, "GET", "/x", authed).Header().Get("X-Cache"))

	// public opts the response into the shared cache -> forced and cached.
	pub := origin(originSpec{body: []byte("pub"), header: http.Header{"Cache-Control": {"public"}}}, new(int32))
	assert.Equal(t, "MISS", do(c, pub, "GET", "/y", authed).Header().Get("X-Cache"))
	assert.Equal(t, "HIT", do(c, pub, "GET", "/y", authed).Header().Get("X-Cache"))
}

// OverrideBalanced does not inspect the request Cookie (and the key ignores it):
// a session-cookie-gated response with no per-user *response* markers is cached
// and served across users. This documents why forcing must target non-per-user
// paths — the Authorization gate alone does not make Balanced safe.
func TestCache_Override_BalancedIgnoresRequestCookie(t *testing.T) {
	c := New(NewMemory(1<<20), Options{
		MaxFileSize: 1024,
		Override:    func(_ *http.Request, _ int, _ http.Header) *Override { return &Override{TTL: time.Hour} },
	})
	o := origin(originSpec{body: []byte("user-a-data")}, new(int32)) // no Set-Cookie/private/no-store

	assert.Equal(t, "MISS", do(c, o, "GET", "/me", http.Header{"Cookie": {"session=a"}}).Header().Get("X-Cache"))
	rec := do(c, o, "GET", "/me", http.Header{"Cookie": {"session=b"}}) // a different user
	assert.Equal(t, "HIT", rec.Header().Get("X-Cache"))
	assert.Equal(t, "user-a-data", rec.Body.String())
}

// OverrideAggressive intentionally bypasses the Authorization gate: a response to
// an authed request is keyed without the credential and served to other users.
// This codifies the documented footgun (use only on non-sensitive endpoints).
func TestCache_Override_AggressiveCachesAcrossUsers(t *testing.T) {
	c := New(NewMemory(1<<20), Options{
		MaxFileSize: 1024,
		Override:    func(_ *http.Request, _ int, _ http.Header) *Override { return &Override{TTL: time.Hour, Mode: OverrideAggressive} },
	})
	o := origin(originSpec{body: []byte("user-data")}, new(int32))

	assert.Equal(t, "MISS", do(c, o, "GET", "/x", http.Header{"Authorization": {"Bearer a"}}).Header().Get("X-Cache"))
	rec := do(c, o, "GET", "/x", nil) // different (unauthenticated) caller, same URL
	assert.Equal(t, "HIT", rec.Header().Get("X-Cache"))
	assert.Equal(t, "user-data", rec.Body.String())
}

// End-to-end: force caching per host, keyed on the request, for an origin that
// sends no cache headers — and confirm the forced policy doesn't leak downstream.
func TestCache_Override_PerHost(t *testing.T) {
	c := New(NewMemory(1<<20), Options{
		MaxFileSize: 1024,
		Override: func(r *http.Request, _ int, _ http.Header) *Override {
			if r.Host == "a.example.com" {
				return &Override{TTL: time.Hour}
			}
			return nil // host b: honor origin
		},
	})
	o := origin(originSpec{body: []byte("x")}, new(int32)) // origin sends no Cache-Control

	// Host A: forced -> cached.
	assert.Equal(t, "MISS", do(c, o, "GET", "http://a.example.com/app.js", nil).Header().Get("X-Cache"))
	recA := do(c, o, "GET", "http://a.example.com/app.js", nil)
	assert.Equal(t, "HIT", recA.Header().Get("X-Cache"))
	assert.Empty(t, recA.Header().Get("Cache-Control"), "forced TTL must not appear in the served header")

	// Host B: honored -> never cached (origin gave no freshness).
	assert.Equal(t, "MISS", do(c, o, "GET", "http://b.example.com/app.js", nil).Header().Get("X-Cache"))
	assert.Equal(t, "MISS", do(c, o, "GET", "http://b.example.com/app.js", nil).Header().Get("X-Cache"))
}

// The Override hook can decide using the origin's response: force only
// successful image responses.
func TestCache_Override_ResponseAware(t *testing.T) {
	c := New(NewMemory(1<<20), Options{
		MaxFileSize: 1 << 20,
		Override: func(_ *http.Request, status int, header http.Header) *Override {
			if status == http.StatusOK && strings.HasPrefix(header.Get("Content-Type"), "image/") {
				return &Override{TTL: time.Hour}
			}
			return nil
		},
	})

	// image/png 200 -> forced, cached.
	img := origin(originSpec{body: []byte("PNG"), header: http.Header{"Content-Type": {"image/png"}}}, new(int32))
	assert.Equal(t, "MISS", do(c, img, "GET", "/a.png", nil).Header().Get("X-Cache"))
	assert.Equal(t, "HIT", do(c, img, "GET", "/a.png", nil).Header().Get("X-Cache"))

	// text/html 200 -> not forced (origin gave no freshness), not cached.
	html := origin(originSpec{body: []byte("<html>"), header: http.Header{"Content-Type": {"text/html"}}}, new(int32))
	assert.Equal(t, "MISS", do(c, html, "GET", "/b.html", nil).Header().Get("X-Cache"))
	assert.Equal(t, "MISS", do(c, html, "GET", "/b.html", nil).Header().Get("X-Cache"))

	// image/* but a 404 -> hook requires 200, so honor origin (no freshness) -> not cached.
	miss := origin(originSpec{status: http.StatusNotFound, body: []byte("no"), header: http.Header{"Content-Type": {"image/png"}}}, new(int32))
	assert.Equal(t, "MISS", do(c, miss, "GET", "/c.png", nil).Header().Get("X-Cache"))
	assert.Equal(t, "MISS", do(c, miss, "GET", "/c.png", nil).Header().Get("X-Cache"))
}

// Balanced overrides the origin's no-cache, but the client still sees the
// origin's header (the override is private to the cache).
func TestCache_Override_OverridesNoCacheKeepsHeader(t *testing.T) {
	c := New(NewMemory(1<<20), Options{
		MaxFileSize: 1024,
		Override:    func(_ *http.Request, _ int, _ http.Header) *Override { return &Override{TTL: time.Hour} },
	})
	o := origin(originSpec{body: []byte("x"), header: http.Header{"Cache-Control": {"no-cache"}}}, new(int32))

	assert.Equal(t, "MISS", do(c, o, "GET", "/x", nil).Header().Get("X-Cache"))
	rec := do(c, o, "GET", "/x", nil)
	assert.Equal(t, "HIT", rec.Header().Get("X-Cache"))
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"), "origin's header is served unchanged")
}

// A nil return honors the origin exactly as if no hook were set.
func TestCache_Override_NilHonorsOrigin(t *testing.T) {
	c := New(NewMemory(1<<20), Options{
		MaxFileSize: 1024,
		Override:    func(_ *http.Request, _ int, _ http.Header) *Override { return nil },
	})
	o := origin(originSpec{body: []byte("x")}, new(int32)) // no freshness

	assert.Equal(t, "MISS", do(c, o, "GET", "/x", nil).Header().Get("X-Cache"))
	assert.Equal(t, "MISS", do(c, o, "GET", "/x", nil).Header().Get("X-Cache"))
}

// A forced stale-if-error window (origin sends nothing) drives serve-stale-on-error.
func TestCache_Override_ForcedStaleIfError(t *testing.T) {
	c := New(NewMemory(1<<20), Options{
		MaxFileSize: 1024,
		Override:    func(_ *http.Request, _ int, _ http.Header) *Override { return &Override{TTL: time.Hour, StaleIfError: time.Hour} },
	})
	do(c, origin(originSpec{body: []byte("old")}, new(int32)), "GET", "/x", nil) // fill (forced)

	// Age it into staleness, keeping the forced window.
	req := mustGet("/x")
	key := c.variantHash(c.primaryHash(req), req)
	m, body, ok := c.storage.Get(key)
	require.True(t, ok)
	require.Equal(t, int64(time.Hour), m.StaleIfError)
	m.FreshUntil = time.Now().Add(-time.Minute).UnixNano()
	storePut(t, c.storage, key, m, body)

	rec := do(c, origin(originSpec{status: http.StatusInternalServerError, body: []byte("boom")}, new(int32)), "GET", "/x", nil)
	assert.Equal(t, "STALE", rec.Header().Get("X-Cache"))
	assert.Equal(t, "old", rec.Body.String())
}

func mustGet(target string) *http.Request {
	return httptest.NewRequest("GET", target, nil)
}
