package h2push

import "net/http"

// Push pushs a link
func Push(link string) LinkPusher {
	return LinkPusher{Link: link}
}

// LinkPusher pushes a link
type LinkPusher struct {
	Link string
}

// ServeHandler implements middleware interface
func (m LinkPusher) ServeHandler(h http.Handler) http.Handler {
	if m.Link == "" {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := w.(http.Pusher); ok {
			p.Push(m.Link, nil)
		}

		h.ServeHTTP(w, r)
	})
}
