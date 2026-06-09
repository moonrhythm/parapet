package block_test

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/block"
	"github.com/moonrhythm/parapet/pkg/headers"
	"github.com/moonrhythm/parapet/pkg/ratelimit"
)

// Apply an inner middleware chain only to requests the Match predicate selects;
// every other request falls through to the rest of the server untouched. Here,
// requests under /api get their own rate limit and an extra request header.
func ExampleNew() {
	b := block.New(func(r *http.Request) bool {
		return strings.HasPrefix(r.URL.Path, "/api/")
	})
	b.Use(ratelimit.FixedWindowPerSecond(20))
	b.Use(headers.SetRequest("X-Scope", "api"))

	s := parapet.New()
	s.Use(b)
	// s.Use(upstream.SingleHost("10.0.0.1:8080")) — handles both matched and
	// unmatched requests; the block only adds behavior for the matched ones.
}

// A nil Match makes the Block a catch-all: its inner chain runs for every
// request. This is a convenient way to group a sub-chain as a single Middleware.
func ExampleNew_catchAll() {
	b := block.New(nil)
	b.Use(headers.SetResponse("X-Served-By", "edge"))

	s := parapet.New()
	s.Use(b)
}

// UseFunc adds an inline MiddlewareFunc to the block's inner chain without
// declaring a named Middleware type.
func ExampleBlock_UseFunc() {
	b := block.New(func(r *http.Request) bool {
		return r.Method == http.MethodPost
	})
	b.UseFunc(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Write", "true")
			h.ServeHTTP(w, r)
		})
	})

	s := parapet.New()
	s.Use(b)
}
