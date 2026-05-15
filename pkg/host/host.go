package host

import (
	"net"
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet/pkg/block"
)

// New creates new host block
func New(host ...string) *block.Block {
	// build host map (normalized: lowercase, no port, no trailing dot)
	hostMap := make(map[string]bool)
	for _, x := range host {
		hostMap[normalizeHost(x)] = true
	}

	if len(host) == 0 {
		return block.New(func(_ *http.Request) bool { return false })
	}

	if hostMap["*"] {
		return block.New(nil)
	}

	return block.New(func(r *http.Request) bool {
		h := normalizeHost(r.Host)

		// exact match
		if hostMap[h] {
			return true
		}

		// wildcard subdomains
		for h != "" {
			i := strings.Index(h, ".")
			if i <= 0 {
				break
			}

			if hostMap["*"+h[i:]] {
				return true
			}
			h = h[i+1:]
		}

		return false
	})
}

// normalizeHost lowercases the host, strips any :port, and strips a single
// trailing dot. The matcher is meant to be safe regardless of whether the
// optional ToLower / StripPort middlewares are installed upstream.
func normalizeHost(h string) string {
	h = strings.ToLower(h)
	if strings.Contains(h, ":") {
		if host, _, err := net.SplitHostPort(h); err == nil {
			h = host
		} else if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
			// bare bracketed IPv6 literal without a port; SplitHostPort
			// rejects it, so unwrap manually so it normalizes the same way
			// as the "[::1]:8080" form.
			h = h[1 : len(h)-1]
		}
	}
	h = strings.TrimSuffix(h, ".")
	return h
}
