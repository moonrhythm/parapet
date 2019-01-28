package upstream

import (
	"github.com/moonrhythm/parapet/pkg/upstream/transport"
)

// HTTP creates new http upstream
func HTTP(target string) *Upstream {
	return New(target, new(transport.HTTP))
}

// HTTPS creates new https upstream
func HTTPS(target string) *Upstream {
	return New(target, new(transport.HTTPS))
}

// H2C creates new h2c upstream
func H2C(target string) *Upstream {
	return New(target, new(transport.H2C))
}

// Unix creates new unix upstream
func Unix(target string) *Upstream {
	return New(target, new(transport.Unix))
}
