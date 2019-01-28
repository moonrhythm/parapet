package host

import (
	"net"
	"net/http"

	"github.com/moonrhythm/parapet/pkg/block"
)

// NewCIDR creates new CIDR host matcher block
func NewCIDR(pattern ...string) *block.Block {
	var nets []*net.IPNet
	for _, p := range pattern {
		_, n, _ := net.ParseCIDR(p)
		if n != nil {
			nets = append(nets, n)
		}
	}
	if len(nets) == 0 {
		return block.New(func(_ *http.Request) bool { return false })
	}

	return block.New(func(r *http.Request) bool {
		requestHost, _, _ := net.SplitHostPort(r.Host)
		if requestHost == "" {
			requestHost = r.Host
		}
		ip := net.ParseIP(requestHost)
		if ip == nil {
			return false
		}

		for _, n := range nets {
			if n.Contains(ip) {
				return true
			}
		}

		return false
	})
}
