package header

import (
	"net/http"
)

// Headers in canonical format
const (
	AcceptEncoding                = "Accept-Encoding"
	AccessControlAllowCredentials = "Access-Control-Allow-Credentials"
	AccessControlAllowHeaders     = "Access-Control-Allow-Headers"
	AccessControlAllowMethods     = "Access-Control-Allow-Methods"
	AccessControlAllowOrigin      = "Access-Control-Allow-Origin"
	AccessControlExposeHeaders    = "Access-Control-Expose-Headers"
	AccessControlMaxAge           = "Access-Control-Max-Age"
	AccessControlRequestHeaders   = "Access-Control-Request-Headers"
	AccessControlRequestMethod    = "Access-Control-Request-Method"
	Authorization                 = "Authorization"
	ContentEncoding               = "Content-Encoding"
	ContentLength                 = "Content-Length"
	ContentType                   = "Content-Type"
	Origin                        = "Origin"
	RetryAfter                    = "Retry-After"
	SecWebsocketKey               = "Sec-Websocket-Key"
	StrictTransportSecurity       = "Strict-Transport-Security"
	Upgrade                       = "Upgrade"
	Vary                          = "Vary"
	WWWAuthenticate               = "Www-Authenticate"
	XForwardedFor                 = "X-Forwarded-For"
	XForwardedHost                = "X-Forwarded-Host"
	XForwardedMethod              = "X-Forwarded-Method"
	XForwardedProto               = "X-Forwarded-Proto"
	XForwardedURI                 = "X-Forwarded-Uri"
	XRealIP                       = "X-Real-Ip"
	XRequestID                    = "X-Request-Id"
)

func AddIfNotExists(h http.Header, key, value string) {
	for _, v := range h[key] {
		if v == value {
			return
		}
	}
	h[key] = append(h[key], value)
}

func Get(h http.Header, key string) string {
	if h == nil {
		return ""
	}
	v := h[key]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func Exists(h http.Header, key string) bool {
	if h == nil {
		return false
	}
	v := h[key]
	if len(v) == 0 {
		return false
	}
	return v[0] != ""
}

func Del(h http.Header, key string) {
	if h == nil {
		return
	}
	delete(h, key)
}

func Set(h http.Header, key, value string) {
	if h == nil {
		return
	}
	h[key] = []string{value}
}

func Add(h http.Header, key, value string) {
	if h == nil {
		return
	}
	h[key] = append(h[key], value)
}
