package cookie

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/moonrhythm/parapet/pkg/headers"
)

// SetHost creates new host setter
func SetHost(host string) *HostSetter {
	return &HostSetter{Host: host}
}

// HostSetter sets host of Set-Cookie header
type HostSetter struct {
	Host string
}

// ServeHandler implements middleware interface
func (m *HostSetter) ServeHandler(h http.Handler) http.Handler {
	var re *regexp.Regexp
	var host string

	if m.Host != "" {
		host = "domain=" + m.Host
		re = regexp.MustCompile(`domain=[^;]*`)
	} else {
		re = regexp.MustCompile(`domain=[^;\n]*;? ?`)
	}

	return headers.MapResponse("Set-Cookie", func(v string) string {
		x := re.ReplaceAllString(v, host)
		if x == v {
			if x == "" {
				x = host
			} else {
				x += "; " + host
			}
		} else {
			x = strings.TrimSpace(x)
			x = strings.TrimSuffix(x, ";")
		}
		return x
	}).ServeHandler(h)
}
