package proxyprotocol_test

import (
	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/proxyprotocol"
)

// Recover the real client IP behind an L4 load balancer that speaks the PROXY
// protocol. Only connections from the balancer's range may set a client
// address.
func ExampleNew() {
	pp := proxyprotocol.New("10.0.0.0/8") // your load balancer's CIDR(s)

	s := parapet.NewFrontend()
	s.ModifyConnection(pp.ModifyConnection)
	// s.Use(...) the rest of the chain; ratelimit/waf/logger now see the client.
}

// Require every trusted connection to carry a PROXY header — appropriate when
// the balancer always prepends one. A trusted connection without a header is
// rejected instead of served with the balancer's address.
func ExampleModifier_require() {
	pp := proxyprotocol.New("10.0.0.0/8")
	pp.Require = true

	s := parapet.NewFrontend()
	s.ModifyConnection(pp.ModifyConnection)
}
