package stripprefix_test

import (
	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/location"
	"github.com/moonrhythm/parapet/pkg/stripprefix"
	"github.com/moonrhythm/parapet/pkg/upstream"
)

// Remove a leading path prefix before the request reaches the rest of the chain,
// so "/api/users" arrives at the next handler as "/users". Requests whose path
// does not start with the prefix are passed through unchanged.
func ExampleNew() {
	s := parapet.New()
	s.Use(stripprefix.New("/api"))
	s.Use(upstream.SingleHost("10.0.0.1:8080", &upstream.HTTPTransport{}))
}

// Pair with location.Prefix to mount a backend under a path that the backend
// itself does not know about: only /api/* is routed into the block, and the
// "/api" prefix is stripped before proxying so the upstream sees "/users".
func ExampleStripPrefix() {
	api := location.Prefix("/api")
	api.Use(stripprefix.New("/api"))
	api.Use(upstream.SingleHost("10.0.0.1:8080", &upstream.HTTPTransport{}))

	s := parapet.New()
	s.Use(api)
}
