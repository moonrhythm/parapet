package location_test

import (
	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/headers"
	"github.com/moonrhythm/parapet/pkg/location"
	"github.com/moonrhythm/parapet/pkg/upstream"
)

// Route a path prefix to its own middleware chain. Each location.* constructor
// returns a *block.Block: register middleware on it with Use, and that chain runs
// only when the request path matches — otherwise the request falls through to the
// rest of the server. Prefix matches on a path-segment boundary, so "/api" matches
// "/api" and "/api/v1" but not "/apixyz".
func ExamplePrefix() {
	api := location.Prefix("/api")
	api.Use(upstream.SingleHost("10.0.0.1:8080", &upstream.HTTPTransport{}))

	s := parapet.New()
	s.Use(api)
	// requests outside /api continue past the block to whatever follows.
}

// Match a single path exactly. Exact("/healthz") matches "/healthz" only — not
// "/healthz/" or "/healthz/live".
func ExampleExact() {
	health := location.Exact("/healthz")
	health.Use(headers.SetResponse("Cache-Control", "no-store"))

	s := parapet.New()
	s.Use(health)
}

// Match with a regular expression for cases the prefix/exact matchers cannot
// express — here, send requests for static assets to a dedicated upstream. The
// pattern is compiled once with regexp.MustCompile.
func ExampleRegExp() {
	assets := location.RegExp(`\.(?:js|css|png|jpg|svg|woff2?)$`)
	assets.Use(upstream.SingleHost("10.0.0.2:8080", &upstream.HTTPTransport{}))

	s := parapet.New()
	s.Use(assets)
}
