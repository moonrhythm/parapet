package cors_test

import (
	"strings"
	"time"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/cors"
)

// Apply the permissive default policy for a public API: any origin is allowed,
// credentials are not. cors.New emits Access-Control-Allow-Origin: * and answers
// preflight (OPTIONS) requests for you.
func ExampleNew() {
	s := parapet.New()
	s.Use(cors.New())
	// s.Use(upstream.SingleHost(...)) — the handler the policy guards.
}

// Restrict to an explicit allow-list of origins and turn on credentials. With a
// non-wildcard origin the browser will accept Access-Control-Allow-Credentials,
// so cookies and Authorization headers are honored on cross-origin requests.
func ExampleAllowOrigins() {
	s := parapet.New()
	s.Use(&cors.CORS{
		AllowOrigins:     cors.AllowOrigins("https://app.example.com", "https://admin.example.com"),
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE"},
		AllowHeaders:     []string{"Authorization", "Content-Type"},
		ExposeHeaders:    []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour, // cache the preflight result this long
	})
}

// Decide allowed origins dynamically by supplying a custom AllowOriginFunc —
// here, any subdomain of example.com. AllowOrigins is just a convenience that
// builds one of these from a fixed list.
func ExampleAllowOriginFunc() {
	s := parapet.New()
	s.Use(&cors.CORS{
		AllowOrigins: cors.AllowOriginFunc(func(origin string) bool {
			return strings.HasSuffix(origin, ".example.com")
		}),
		AllowMethods: []string{"GET", "POST"},
		AllowHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:       time.Hour,
	})
}
