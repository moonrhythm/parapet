package headers_test

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/headers"
)

// Set response headers, overwriting any existing values. Pairs are given as
// key, value, key, value... Use SetRequest to rewrite headers on the way in to
// the upstream instead.
func ExampleSetResponse() {
	s := parapet.New()
	s.Use(headers.SetResponse(
		"X-Frame-Options", "DENY",
		"X-Content-Type-Options", "nosniff",
	))
	// s.Use(upstream.SingleHost("10.0.0.1:8080")) — the proxied backend.
}

// Add request headers, appending to any existing values rather than replacing
// them. Handy for flags a backend may set more than once.
func ExampleAddRequest() {
	s := parapet.New()
	s.Use(headers.AddRequest("X-Forwarded-Proto", "https"))
}

// Strip hop-by-hop or sensitive headers from the upstream's response before it
// reaches the client.
func ExampleDeleteResponse() {
	s := parapet.New()
	s.Use(headers.DeleteResponse("Server", "X-Powered-By"))
}

// Rewrite each value of a request header in place. Here the inbound Host is
// lower-cased before it is proxied; MapRequest only touches values that are
// already present.
func ExampleMapRequest() {
	s := parapet.New()
	s.Use(headers.MapRequest("Host", strings.ToLower))
}

// InterceptResponse runs arbitrary logic against the response headers, with
// access to the final status code via the ResponseHeaderWriter. Here a
// long-lived Cache-Control is added only to successful responses.
func ExampleInterceptResponse() {
	s := parapet.New()
	s.Use(headers.InterceptResponse(func(w headers.ResponseHeaderWriter) {
		if w.StatusCode() == http.StatusOK {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
	}))
}
