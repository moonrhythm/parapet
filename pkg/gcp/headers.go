package gcp

import (
	"net/http"
	"strings"

	"github.com/moonrhythm/parapet/pkg/headers"
)

// HLBImmediateIP extracts client ip from gcp hlb and set to X-Real-Ip
func HLBImmediateIP(proxy int) headers.RequestInterceptor {
	if proxy < 0 {
		proxy = 0
	}

	return headers.InterceptRequest(func(h http.Header) {
		xff := h.Get("X-Forwarded-For")
		if len(xff) == 0 {
			return
		}

		xs := strings.Split(xff, ",")
		if len(xs) < 2+proxy {
			return
		}

		h.Set("X-Real-Ip", strings.TrimSpace(xs[len(xs)-2-proxy]))
	})
}
