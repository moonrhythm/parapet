package healthz

import "net/http"

// Healthz middleware
type Healthz struct{}

// New creates new healthz
func New() Healthz {
	return Healthz{}
}

// ServeHandler implements middleware interface
func (m Healthz) ServeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
}
