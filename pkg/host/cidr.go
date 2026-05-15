package host

import (
	"net"
	"net/http"
	"strconv"

	"github.com/moonrhythm/parapet/pkg/block"
)

// NewCIDR creates new CIDR host matcher block.
//
// Panics on any unparsable CIDR so a configuration typo cannot silently
// collapse the matcher to an empty (always-false) set.
func NewCIDR(pattern ...string) *block.Block {
	if len(pattern) == 0 {
		return block.New(func(_ *http.Request) bool { return false })
	}
	nets := make([]*net.IPNet, 0, len(pattern))
	for _, p := range pattern {
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			panic("host: invalid CIDR " + strconv.Quote(p) + ": " + err.Error())
		}
		nets = append(nets, n)
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
