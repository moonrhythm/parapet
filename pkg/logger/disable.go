package logger

import (
	"net/http"

	"github.com/moonrhythm/parapet"
)

// Disable disables log
func Disable() parapet.Middleware {
	return disable{}
}

type disable struct{}

func (m disable) ServeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		d := getRecord(ctx)
		if d != nil {
			d.disable = true
		}

		h.ServeHTTP(w, r)
	})
}
