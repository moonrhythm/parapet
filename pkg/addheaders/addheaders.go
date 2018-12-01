package addheaders

import "net/http"

// AddHeaders adds headers before send to upstream
type AddHeaders struct {
	Headers []Header
}

// Header type
type Header struct {
	Key   string
	Value string
}

// ServeHandler implements middleware interface
func (m *AddHeaders) ServeHandler(h http.Handler) http.Handler {
	if len(m.Headers) == 0 {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range m.Headers {
			w.Header().Add(p.Key, p.Value)
		}

		h.ServeHTTP(w, r)
	})
}
