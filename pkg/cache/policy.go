package cache

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxTTL caps the cacheable lifetime so now+ttl stays well within
// time.UnixNano's representable range (~year 2262). 10y exceeds any real TTL.
const maxTTL = 10 * 365 * 24 * time.Hour

// cacheableMethod reports the only methods the cache engages for.
func cacheableMethod(m string) bool { return m == http.MethodGet || m == http.MethodHead }

// isUpgrade reports a protocol-upgrade request (websocket, h2c upgrade). Such
// requests are streamed and must never be cached.
func isUpgrade(r *http.Request) bool {
	if r.Header.Get("Upgrade") != "" {
		return true
	}
	for _, v := range r.Header.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "upgrade") {
				return true
			}
		}
	}
	return false
}

// cacheableStatus is the set of status codes the cache will store (a conservative
// subset of the RFC "heuristically cacheable" set; 206/partial is excluded — no
// Range support). Freshness is still required regardless.
func cacheableStatus(code int) bool {
	switch code {
	case 200, 203, 204, 300, 301, 308, 404, 410:
		return true
	default:
		return false
	}
}

// decision is the outcome of the honor-origin cacheability check.
//
//nolint:govet
type decision struct {
	cacheable  bool
	freshUntil time.Time
	vary       []string // lowercased Vary header names (nil when no Vary)
}

// decide applies the honor-origin policy to an origin response. method is the
// request method; status/h are the response status + headers; reqAuthorized
// reports whether the request carried an Authorization header; maxFileSize caps a
// GET's Content-Length; now is the reference time for freshness. Returns
// cacheable=false unless the origin explicitly opted in with freshness and the
// response isn't refused for any reason.
//
// A GET is cacheable only with a Content-Length within the cap: the middleware
// commits a body only once written bytes == Content-Length, guaranteeing a
// truncated response is never stored. A chunked (no Content-Length) GET passes
// through uncached. HEAD has no body and is unaffected.
func decide(method string, status int, h http.Header, reqAuthorized bool, maxFileSize int64, now time.Time) decision {
	no := decision{}
	if !cacheableStatus(status) {
		return no
	}
	// A Set-Cookie response is per-client; never store it in a shared cache.
	if len(h.Values("Set-Cookie")) > 0 {
		return no
	}
	vary, varyStar := parseVary(h)
	if varyStar {
		return no // Vary: * — no single key can represent it
	}
	cc := parseCacheControl(h)
	if cc.private || cc.noStore || cc.noCache {
		return no
	}
	// RFC 9111 §3.5: a shared cache MUST NOT store/reuse a response to an
	// Authorization-bearing request unless the response explicitly opts in via
	// public, s-maxage, or must-revalidate. The honor-origin policy otherwise keys
	// without Authorization, so without this gate a bare max-age response to an
	// authenticated request would be served to other users — a cross-user leak.
	sharedOptIn := cc.public || cc.hasSMax || cc.mustRevalidate
	if reqAuthorized && !sharedOptIn {
		return no
	}
	ttl := freshness(cc, h, now)
	if ttl <= 0 {
		return no // honor-origin: no explicit freshness -> not cached
	}
	// Clamp absurd freshness so now+ttl stays within time.UnixNano's range.
	if ttl > maxTTL {
		ttl = maxTTL
	}
	if method == http.MethodGet {
		cl, ok := contentLength(h)
		if !ok || cl < 0 || cl > maxFileSize {
			return no
		}
	}
	return decision{cacheable: true, freshUntil: now.Add(ttl), vary: vary}
}

// parseVary returns the lowercased, de-duplicated Vary header names and whether
// any token was "*".
func parseVary(h http.Header) (names []string, star bool) {
	seen := map[string]struct{}{}
	for _, v := range h.Values("Vary") {
		for _, tok := range strings.Split(v, ",") {
			tok = strings.ToLower(strings.TrimSpace(tok))
			if tok == "" {
				continue
			}
			if tok == "*" {
				return nil, true
			}
			if _, dup := seen[tok]; dup {
				continue
			}
			seen[tok] = struct{}{}
			names = append(names, tok)
		}
	}
	return names, false
}

//nolint:govet
type cacheControl struct {
	private        bool
	noStore        bool
	noCache        bool
	public         bool
	mustRevalidate bool
	maxAge         int64
	sMaxAge        int64
	hasMax         bool
	hasSMax        bool
}

// parseCacheControl parses the response Cache-Control directives the policy
// cares about.
func parseCacheControl(h http.Header) cacheControl {
	var cc cacheControl
	for _, v := range h.Values("Cache-Control") {
		for _, raw := range strings.Split(v, ",") {
			d := strings.TrimSpace(raw)
			if d == "" {
				continue
			}
			name, val, _ := strings.Cut(d, "=")
			name = strings.ToLower(strings.TrimSpace(name))
			val = strings.Trim(strings.TrimSpace(val), "\"")
			switch name {
			case "private":
				cc.private = true
			case "no-store":
				cc.noStore = true
			case "no-cache":
				cc.noCache = true
			case "public":
				cc.public = true
			case "must-revalidate":
				cc.mustRevalidate = true
			case "max-age":
				if n, err := strconv.ParseInt(val, 10, 64); err == nil {
					cc.maxAge = n
					cc.hasMax = true
				}
			case "s-maxage":
				if n, err := strconv.ParseInt(val, 10, 64); err == nil {
					cc.sMaxAge = n
					cc.hasSMax = true
				}
			}
		}
	}
	return cc
}

// freshness returns the cacheable lifetime: s-maxage wins (shared-cache
// directive), then max-age, then Expires (relative to Date if present, else
// now). Returns 0 (not fresh) when the origin gave no explicit freshness.
func freshness(cc cacheControl, h http.Header, now time.Time) time.Duration {
	if cc.hasSMax {
		return time.Duration(cc.sMaxAge) * time.Second
	}
	if cc.hasMax {
		return time.Duration(cc.maxAge) * time.Second
	}
	if exp := h.Get("Expires"); exp != "" {
		expTime, err := http.ParseTime(exp)
		if err != nil {
			return 0 // an unparseable Expires (e.g. "0") means already-expired
		}
		ref := now
		if d := h.Get("Date"); d != "" {
			if dt, derr := http.ParseTime(d); derr == nil {
				ref = dt
			}
		}
		return expTime.Sub(ref)
	}
	return 0
}

// contentLength parses the Content-Length header.
func contentLength(h http.Header) (int64, bool) {
	v := h.Get("Content-Length")
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
