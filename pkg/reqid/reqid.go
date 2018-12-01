package reqid

import (
	"net/http"

	"github.com/satori/go.uuid"
)

// ReqID middleware
type ReqID struct {
	TrustProxy bool
	Header     string
}

const defaultHeader = "X-Request-Id"

// ServeHandler implements middleware interface
func (m *ReqID) ServeHandler(h http.Handler) http.Handler {
	if m.Header == "" {
		m.Header = defaultHeader
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(m.Header)
		if id == "" || !m.TrustProxy {
			id = uuid.NewV4().String()
			r.Header.Set(m.Header, id)
		}
		w.Header().Set(m.Header, id)

		h.ServeHTTP(w, r)
	})
}
