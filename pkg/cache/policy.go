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
type decision struct {
	freshUntil time.Time
	vary       []string // lowercased Vary header names (nil when no Vary)
	// staleWhileRevalidate and staleIfError are the RFC 5861 windows past
	// freshUntil during which a stale entry may be served (while revalidating, or
	// on a revalidation error). Zero when not offered.
	staleWhileRevalidate time.Duration
	staleIfError         time.Duration
	cacheable            bool
	// noStale reports that the response forbids serving stale (must-revalidate /
	// proxy-revalidate), so operator-configured default windows must not apply.
	noStale bool
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
func decide(method string, status int, h http.Header, reqAuthorized bool, maxFileSize int64, cacheChunked bool, now time.Time) decision {
	no := decision{}
	vary, ok := storeRefusals(status, h)
	if !ok {
		return no
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
	if reqAuthorized && !sharedOptIn(cc) {
		return no
	}
	ttl := freshness(cc, h, now)
	if ttl <= 0 {
		return no // honor-origin: no explicit freshness -> not cached
	}
	if !fitsCap(method, h, maxFileSize, cacheChunked) {
		return no
	}
	// Clamp absurd freshness so now+ttl stays within time.UnixNano's range.
	if ttl > maxTTL {
		ttl = maxTTL
	}
	noStale := cc.mustRevalidate || cc.proxyRevalidate
	swr, sie := staleWindows(cc)
	return decision{cacheable: true, freshUntil: now.Add(ttl), vary: vary, staleWhileRevalidate: swr, staleIfError: sie, noStale: noStale}
}

// decideForced applies an operator-forced policy (Options.Override) instead of
// honor-origin freshness. The always-refusals (status, Set-Cookie, Vary: *,
// oversize) hold in every mode; ov.Mode chooses how many of the origin's own
// refusals (no-cache, no-store/private, the Authorization gate) still apply and
// whether the forced TTL overrides the origin's freshness. The caller guarantees
// ov.TTL > 0.
func decideForced(method string, status int, h http.Header, reqAuthorized bool, maxFileSize int64, cacheChunked bool, now time.Time, ov *Override) decision {
	no := decision{}
	vary, ok := storeRefusals(status, h)
	if !ok {
		return no
	}
	cc := parseCacheControl(h)

	// Mode-dependent refusals over the origin's Cache-Control.
	switch ov.Mode {
	case OverrideConservative:
		if cc.private || cc.noStore || cc.noCache {
			return no
		}
		if reqAuthorized && !sharedOptIn(cc) {
			return no
		}
	case OverrideBalanced:
		if cc.private || cc.noStore { // honor store-sensitivity, override no-cache
			return no
		}
		if reqAuthorized && !sharedOptIn(cc) {
			return no
		}
	case OverrideAggressive:
		// only the always-refusals apply
	}

	// Freshness: Conservative fills only when the origin gave none; the others
	// override outright.
	ttl := ov.TTL
	if ov.Mode == OverrideConservative {
		if originTTL := freshness(cc, h, now); originTTL > 0 {
			ttl = originTTL
		}
	}
	if ttl <= 0 {
		return no
	}
	if !fitsCap(method, h, maxFileSize, cacheChunked) {
		return no
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}

	// Windows: Conservative honors the origin (with the forced windows filling any
	// gap and must-revalidate still suppressing); the others force the windows.
	var swr, sie time.Duration
	var noStale bool
	if ov.Mode == OverrideConservative {
		swr, sie = staleWindows(cc)
		noStale = cc.mustRevalidate || cc.proxyRevalidate
		// Conservative honors the origin: when it forbids stale serving
		// (must-revalidate / proxy-revalidate) don't fill the forced windows either.
		if !noStale {
			if swr == 0 {
				swr = clampStaleWindow(ov.StaleWhileRevalidate)
			}
			if sie == 0 {
				sie = clampStaleWindow(ov.StaleIfError)
			}
		}
	} else {
		swr = clampStaleWindow(ov.StaleWhileRevalidate)
		sie = clampStaleWindow(ov.StaleIfError)
	}
	return decision{cacheable: true, freshUntil: now.Add(ttl), vary: vary, staleWhileRevalidate: swr, staleIfError: sie, noStale: noStale}
}

// storeRefusals applies the refusals that hold for any cached entry regardless of
// an override: a non-cacheable status, a per-client Set-Cookie, and Vary: * (no
// single key can represent it). It returns the parsed Vary names and ok=false on
// a refusal.
func storeRefusals(status int, h http.Header) (vary []string, ok bool) {
	if !cacheableStatus(status) {
		return nil, false
	}
	if len(h.Values("Set-Cookie")) > 0 {
		return nil, false
	}
	vary, varyStar := parseVary(h)
	if varyStar {
		return nil, false
	}
	return vary, true
}

// sharedOptIn reports the RFC 9111 §3.5 opt-in that lets a shared cache store a
// response to an Authorization-bearing request.
func sharedOptIn(cc cacheControl) bool {
	return cc.public || cc.hasSMax || cc.mustRevalidate
}

// fitsCap reports whether a GET response is within the per-object cap (HEAD has
// no body and always fits). With a Content-Length, it must be present and within
// the cap. Without one (a chunked / streamed GET), the response is cacheable only
// when cacheChunked is enabled — and never for a Server-Sent-Events stream; the
// real cap is then enforced mid-stream by the teeWriter, and completeness comes
// from the upstream handler finishing cleanly (see teeWriter.finish). When
// cacheChunked is off, a no-Content-Length GET is not cacheable, as before.
func fitsCap(method string, h http.Header, maxFileSize int64, cacheChunked bool) bool {
	if method != http.MethodGet {
		return true
	}
	cl, ok := contentLength(h)
	if !ok {
		return cacheChunked && !isEventStream(h)
	}
	return cl >= 0 && cl <= maxFileSize
}

// isEventStream reports a Server-Sent-Events response (Content-Type
// text/event-stream, with or without parameters), which is an open-ended stream
// and must never be buffered for caching.
func isEventStream(h http.Header) bool {
	ct := strings.ToLower(strings.TrimSpace(h.Get("Content-Type")))
	return ct == "text/event-stream" || strings.HasPrefix(ct, "text/event-stream;")
}

// staleWindows derives the RFC 5861 stale-serving windows from the response
// Cache-Control. must-revalidate / proxy-revalidate forbid serving stale at all
// (RFC 9111 §4.2.4), so they suppress both windows. Each is clamped to maxTTL so
// FreshUntil+window stays within time.UnixNano's range.
func staleWindows(cc cacheControl) (swr, sie time.Duration) {
	if cc.mustRevalidate || cc.proxyRevalidate {
		return 0, 0
	}
	return clampStaleWindow(time.Duration(cc.staleWhileRevalidate) * time.Second),
		clampStaleWindow(time.Duration(cc.staleIfError) * time.Second)
}

// clampStaleWindow bounds a stale window to [0, maxTTL] so FreshUntil+window
// stays within time.UnixNano's range; a negative duration is dropped to zero.
func clampStaleWindow(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if d > maxTTL {
		return maxTTL
	}
	return d
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

type cacheControl struct {
	maxAge               int64
	sMaxAge              int64
	staleWhileRevalidate int64
	staleIfError         int64
	private              bool
	noStore              bool
	noCache              bool
	public               bool
	mustRevalidate       bool
	proxyRevalidate      bool
	hasMax               bool
	hasSMax              bool
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
			case "proxy-revalidate":
				cc.proxyRevalidate = true
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
			case "stale-while-revalidate":
				if n, err := strconv.ParseInt(val, 10, 64); err == nil && n >= 0 {
					cc.staleWhileRevalidate = n
				}
			case "stale-if-error":
				if n, err := strconv.ParseInt(val, 10, 64); err == nil && n >= 0 {
					cc.staleIfError = n
				}
			}
		}
	}
	return cc
}

// freshness returns the remaining cacheable lifetime from now: the origin's
// declared freshness lifetime minus the age the response already had when it was
// received. Accounting for that age (RFC 9111 §4.2.3) keeps the cache from
// over-serving a response that was already aged upstream — e.g. max-age=60 on a
// response carrying Age: 55 is fresh for ~5s, not a full 60s. Returns <= 0
// (treated as not cacheable) when the origin gave no explicit freshness or the
// response is already stale on arrival.
func freshness(cc cacheControl, h http.Header, now time.Time) time.Duration {
	lifetime := freshnessLifetime(cc, h, now)
	if lifetime <= 0 {
		return lifetime // no explicit freshness, or already-expired
	}
	return lifetime - responseAge(h, now)
}

// freshnessLifetime returns the origin's declared freshness lifetime: s-maxage
// wins (shared-cache directive), then max-age, then Expires (relative to Date if
// present, else now). Returns 0 when the origin gave no explicit freshness.
func freshnessLifetime(cc cacheControl, h http.Header, now time.Time) time.Duration {
	if cc.hasSMax {
		return time.Duration(cc.sMaxAge) * time.Second
	}
	if cc.hasMax {
		return time.Duration(cc.maxAge) * time.Second
	}
	exp := h.Get("Expires")
	if exp == "" {
		return 0
	}
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

// responseAge estimates how old the response already was when received: the larger
// of the Age header value and the apparent age (now - Date). Per RFC 9111 §4.2.3
// this is the corrected initial age at receipt; resident time afterwards is
// tracked by the stored FreshUntil clock. Never negative.
func responseAge(h http.Header, now time.Time) time.Duration {
	var apparent time.Duration
	if d := h.Get("Date"); d != "" {
		if dt, err := http.ParseTime(d); err == nil {
			if a := now.Sub(dt); a > apparent {
				apparent = a
			}
		}
	}
	var ageVal time.Duration
	if v := h.Get("Age"); v != "" {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
			ageVal = time.Duration(n) * time.Second
		}
	}
	if ageVal > apparent {
		return ageVal
	}
	return apparent
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
