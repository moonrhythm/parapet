package redirect_test

import (
	"net/http"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/redirect"
)

// Redirect plain HTTP requests to their HTTPS equivalent. The scheme is read
// from X-Forwarded-Proto, so place this behind a TLS-terminating load balancer.
func ExampleHTTPS() {
	s := parapet.New()
	s.Use(redirect.HTTPS())
	// s.Use(upstream.SingleHost(...)) — handles requests that already arrived over HTTPS.
}

// Send a 308 Permanent Redirect instead of the default 301 so the browser
// preserves the request method and body when retrying.
func ExampleHTTPS_permanent() {
	m := redirect.HTTPS()
	m.StatusCode = http.StatusPermanentRedirect

	s := parapet.New()
	s.Use(m)
}

// Normalize "www.example.com" to the bare "example.com" host, keeping the
// request's scheme, path and query.
func ExampleNonWWW() {
	s := parapet.New()
	s.Use(redirect.NonWWW())
}

// Normalize the bare host to its "www." variant — the inverse of NonWWW; pick
// one canonical form for your site.
func ExampleWWW() {
	s := parapet.New()
	s.Use(redirect.WWW())
}

// Redirect every request to a fixed target with an explicit status code, e.g.
// when retiring a domain.
func ExampleTo() {
	s := parapet.New()
	s.Use(redirect.To("https://new.example.com/", http.StatusMovedPermanently))
}
