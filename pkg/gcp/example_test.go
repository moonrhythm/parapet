package gcp_test

import (
	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/gcp"
)

// Behind a GCP HTTP(S) Load Balancer the real client IP is the second-to-last
// entry of X-Forwarded-For (the last is the LB itself). HLBImmediateIP copies
// that address into X-Real-Ip so downstream middleware can trust it. With no
// additional proxies in front of the LB, pass proxy = 0.
func ExampleHLBImmediateIP() {
	s := parapet.NewFrontend()
	s.Use(gcp.HLBImmediateIP(0))
	// s.Use(upstream.SingleHost("10.0.0.1:8080")) — the backend handler.
}

// When extra reverse proxies sit between the client and the GCP load balancer,
// each appends its own hop to X-Forwarded-For. Pass the number of those extra
// hops as proxy so the correct client address is selected.
func ExampleHLBImmediateIP_extraProxies() {
	s := parapet.NewFrontend()
	s.Use(gcp.HLBImmediateIP(1)) // one proxy hop ahead of the GCP HLB
}
