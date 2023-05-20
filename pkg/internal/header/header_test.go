package header_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet/pkg/internal/header"
)

func TestHeaders(t *testing.T) {
	list := []string{
		header.AcceptEncoding,
		header.AccessControlAllowCredentials,
		header.AccessControlAllowHeaders,
		header.AccessControlAllowMethods,
		header.AccessControlAllowOrigin,
		header.AccessControlExposeHeaders,
		header.AccessControlMaxAge,
		header.AccessControlRequestHeaders,
		header.AccessControlRequestMethod,
		header.Authorization,
		header.ContentEncoding,
		header.ContentLength,
		header.ContentType,
		header.Origin,
		header.RetryAfter,
		header.SecWebsocketKey,
		header.StrictTransportSecurity,
		header.Upgrade,
		header.Vary,
		header.WWWAuthenticate,
		header.XForwardedFor,
		header.XForwardedHost,
		header.XForwardedMethod,
		header.XForwardedProto,
		header.XForwardedURI,
		header.XRealIP,
		header.XRequestID,
	}

	for _, x := range list {
		assert.Equal(t, http.CanonicalHeaderKey(x), x)
	}
}
