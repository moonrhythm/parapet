package requestid

import (
	"net/http"

	"github.com/gofrs/uuid"

	"github.com/moonrhythm/parapet/pkg/header"
	"github.com/moonrhythm/parapet/pkg/logger"
)

// RequestID middleware
//
//nolint:govet
type RequestID struct {
	// TrustProxy trusts request id from request header
	// sets TrustProxy to false for always generate new request id
	TrustProxy bool

	// Header is the http header key
	Header string
}

// New creates default req id middleware
func New() *RequestID {
	return &RequestID{
		TrustProxy: true,
	}
}

// DefaultHeader is the default request, response header
const DefaultHeader = header.XRequestID

// maxClientRequestIDLen caps the length of a client-supplied request id so a
// caller cannot pass an unbounded blob into logs/upstream headers.
const maxClientRequestIDLen = 128

// ServeHandler implements middleware interface
func (m RequestID) ServeHandler(h http.Handler) http.Handler {
	if m.Header == "" {
		m.Header = DefaultHeader
	}
	m.Header = http.CanonicalHeaderKey(m.Header)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := header.Get(r.Header, m.Header)
		if id == "" || !m.TrustProxy || !isValidRequestID(id) {
			id = uuid.Must(uuid.NewV4()).String()
			header.Set(r.Header, m.Header, id)
		}
		header.Set(w.Header(), m.Header, id)
		logger.Set(r.Context(), "requestId", id)

		h.ServeHTTP(w, r)
	})
}

// isValidRequestID accepts only a conservative charset so a client cannot
// inject control characters or oversized values into logs and upstream headers.
func isValidRequestID(s string) bool {
	if s == "" || len(s) > maxClientRequestIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c == '-' || c == '_' || c == '.' || c == ':' || c == '+' || c == '=' || c == '/':
		default:
			return false
		}
	}
	return true
}
