package h2push

import (
	"net/http"

	"github.com/tomnomnom/linkheader"
)

// Preload creates new PreloadPusher
func Preload() *PreloadPusher {
	return new(PreloadPusher)
}

// PreloadPusher pushs preload link
type PreloadPusher struct{}

// ServeHandler implements middleware interface
func (m *PreloadPusher) ServeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// skip if not support pusher
		_, ok := w.(http.Pusher)
		if !ok {
			h.ServeHTTP(w, r)
			return
		}

		nw := preloadPusherRW{
			ResponseWriter: w,
			header:         make(http.Header),
		}
		h.ServeHTTP(&nw, r)
	})
}

type preloadPusherRW struct {
	http.ResponseWriter

	wroteHeader bool
	header      http.Header
}

func (w *preloadPusherRW) Header() http.Header {
	return w.header
}

func (w *preloadPusherRW) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	p := w.ResponseWriter.(http.Pusher)
	ll := linkheader.ParseMultiple(w.header["Link"])
	for _, l := range ll {
		if l.HasParam("nopush") {
			continue
		}
		if l.Param("rel") != "preload" {
			continue
		}

		p.Push(l.URL, nil)
	}

	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *preloadPusherRW) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(p)
}
