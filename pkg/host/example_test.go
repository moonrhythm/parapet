package host_test

import (
	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/host"
)

// Route by virtual host: host.New returns a block that only runs its own inner
// chain when the request's Host matches. Names are matched case-insensitively
// and ignore any :port; a leading "*." matches all subdomains and a lone "*"
// matches every host.
func ExampleNew() {
	s := parapet.New()

	// app.example.com and all of its subdomains go to one upstream...
	app := host.New("app.example.com", "*.app.example.com")
	// app.Use(upstream.SingleHost("10.0.0.1:8080", nil))
	s.Use(app)

	// ...while the bare apex domain goes somewhere else.
	web := host.New("example.com")
	// web.Use(upstream.SingleHost("10.0.0.2:8080", nil))
	s.Use(web)
}

// Route by client IP instead of name: host.NewCIDR matches when the request's
// Host is an IP literal inside one of the given CIDR ranges. It panics on an
// unparsable CIDR, so a config typo fails loudly at startup.
func ExampleNewCIDR() {
	s := parapet.New()

	internal := host.NewCIDR("10.0.0.0/8", "192.168.0.0/16")
	// internal.Use(...) — handlers reachable only from the private network.
	s.Use(internal)
}

// Normalize the request host before any host-based routing runs: lowercase it
// and drop the :port. host.New already normalizes internally, but installing
// these first means every downstream middleware sees a clean Host too.
func ExampleStripPort() {
	s := parapet.New()
	s.Use(host.ToLower())
	s.Use(host.StripPort())

	s.Use(host.New("example.com"))
}
