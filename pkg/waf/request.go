package waf

import (
	"net"
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet/pkg/header"
)

// buildRequestMap builds the CEL `request` map from an *http.Request.
//
// We materialise the map eagerly (one allocation up-front) so that the same
// map can be passed to every Rule.Program in a ruleset without re-walking
// the request headers. Body inspection is opt-in via the `body` argument
// (already truncated by the caller).
//
// The map keys are deliberately snake_case to match common WAF terminology
// (e.g. ModSecurity variable names) and to leave camelCase free for any
// future user-defined fields a caller may merge into the map.
func buildRequestMap(r *http.Request, body, country string) map[string]any {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if v := header.Get(r.Header, header.XForwardedProto); v != "" {
		scheme = v
	}

	headers := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		if len(v) == 0 {
			continue
		}
		// Lowercase keys make case-insensitive header lookups in rules safe
		// without forcing every rule author to remember canonical casing.
		headers[strings.ToLower(k)] = v[0]
	}

	// Best-effort cookie parsing. We never error here — a malformed Cookie
	// header should not crash a WAF; instead the rule simply sees fewer
	// entries.
	cookies := map[string]string{}
	for _, c := range r.Cookies() {
		// Last write wins on duplicate names; this matches net/http's own
		// Request.Cookie behaviour.
		cookies[c.Name] = c.Value
	}

	args := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) == 0 {
			continue
		}
		args[k] = v[0]
	}

	return map[string]any{
		requestVar: map[string]any{
			"method":         r.Method,
			"host":           r.Host,
			"path":           r.URL.Path,
			"query":          r.URL.RawQuery,
			"uri":            r.RequestURI,
			"proto":          r.Proto,
			"scheme":         scheme,
			"remote_ip":      clientIP(r),
			"country":        country,
			"content_length": r.ContentLength,
			"headers":        headers,
			"cookies":        cookies,
			"args":           args,
			"user_agent":     r.UserAgent(),
			"referer":        r.Referer(),
			"body":           body,
		},
	}
}

// clientIP returns the best-known client IP, preferring trusted proxy
// headers when populated by parapet's proxy layer.
func clientIP(r *http.Request) string {
	if v := header.Get(r.Header, header.XRealIP); v != "" {
		return v
	}
	if v := header.Get(r.Header, header.XForwardedFor); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return v
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
