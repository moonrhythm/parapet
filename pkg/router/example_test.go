package router_test

import (
	"net/http"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/router"
)

// mw is a tiny helper that builds a parapet.Middleware which always responds
// with the given body, standing in for a real backend (e.g. upstream.SingleHost).
func mw(body string) parapet.Middleware {
	return parapet.MiddlewareFunc(func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		})
	})
}

// Dispatch requests to different inner middleware chains by URL path prefix, then
// wire the router into a parapet server. Each pattern matches itself and anything
// beneath it ("/api" also serves "/api/v1/..."); unmatched paths fall through to
// whatever the server runs after the router.
func ExampleNew() {
	r := router.New()
	r.Handle("/api", mw("api"))    // /api and /api/...
	r.Handle("/static", mw("cdn")) // /static and /static/...

	s := parapet.New()
	s.Use(r)
}

// Registering "/" overrides the fall-through: every request that no other pattern
// claims is handled by this middleware instead of the server's later handlers.
func ExampleRouter_Handle_root() {
	r := router.New()
	r.Handle("/healthz", mw("ok"))
	r.Handle("/", mw("default")) // catch-all for everything else

	s := parapet.New()
	s.Use(r)
}
