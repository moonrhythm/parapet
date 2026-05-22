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
			i := strings.IndexByte(h, '.')
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
//
// The fast path detects an already-normalized input (lowercase ASCII, no
// colon, no trailing dot) in a single scan and returns it unchanged. The
// slow path falls back to the general handling and matches the original
// behavior — port stripping and bracket unwrapping only run when the input
// actually contains a ':'.
func normalizeHost(h string) string {
	if h == "" {
		return h
	}

	var (
		needsLower bool
		hasColon   bool
	)
	n := len(h)
	for i := 0; i < n; i++ {
		c := h[i]
		if c >= 'A' && c <= 'Z' {
			needsLower = true
		} else if c == ':' {
			hasColon = true
		}
	}
	trailingDot := h[n-1] == '.'

	if !needsLower && !hasColon && !trailingDot {
		return h
	}

	return normalizeHostSlow(h, hasColon, trailingDot, needsLower)
}

func normalizeHostSlow(h string, hasColon, trailingDot, needsLower bool) string {
	if hasColon {
		if host, _, err := net.SplitHostPort(h); err == nil {
			h = host
		} else if h[0] == '[' && h[len(h)-1] == ']' {
			// bare bracketed IPv6 literal without a port; SplitHostPort
			// rejects it, so unwrap manually so it normalizes the same way
			// as the "[::1]:8080" form.
			h = h[1 : len(h)-1]
		}
		// after splitting, the trailing dot (if any) might have been
		// stripped along with the port; recompute.
		if len(h) > 0 {
			trailingDot = h[len(h)-1] == '.'
		}
	}
	if trailingDot {
		h = h[:len(h)-1]
	}
	if needsLower {
		h = strings.ToLower(h)
	}
	return h
}
