package reqid

import (
	"net/http"

	"github.com/gofrs/uuid"
)

// ReqID middleware
type ReqID struct {
	TrustProxy bool
	Header     string
}

// New creates default req id middleware
func New() *ReqID {
	return &ReqID{
		TrustProxy: true,
	}
}

// ServeHandler implements middleware interface
func (m *ReqID) ServeHandler(h http.Handler) http.Handler {
	if m.Header == "" {
		m.Header = "X-Request-Id"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(m.Header)
		if id == "" || !m.TrustProxy {
			id = uuid.Must(uuid.NewV4()).String()
			r.Header.Set(m.Header, id)
		}
		w.Header().Set(m.Header, id)

		h.ServeHTTP(w, r)
	})
}
