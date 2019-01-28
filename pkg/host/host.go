package host

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet/pkg/block"
)

// New creates new host block
func New(host ...string) *block.Block {
	// build host map
	hostMap := make(map[string]bool)
	for _, x := range host {
		hostMap[x] = true
	}

	if len(host) == 0 {
		return block.New(func(_ *http.Request) bool { return false })
	}

	if hostMap["*"] {
		return block.New(nil)
	}

	return block.New(func(r *http.Request) bool {
		// exact match
		if hostMap[r.Host] {
			return true
		}

		// wildcard subdomains
		host := r.Host
		for host != "" {
			i := strings.Index(host, ".")
			if i <= 0 {
				break
			}

			if hostMap["*"+host[i:]] {
				return true
			}
			host = host[i+1:]
		}

		return false
	})
}
