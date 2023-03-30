package h2push

import (
	"bufio"
	"net"
	"net/http"

	"github.com/tomnomnom/linkheader"
)

// Preload creates new PreloadPusher
func Preload() *PreloadPusher {
	return new(PreloadPusher)
}

// PreloadPusher pushes preload link
type PreloadPusher struct{}

// ServeHandler implements middleware interface
func (m PreloadPusher) ServeHandler(h http.Handler) http.Handler {
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

func (w *preloadPusherRW) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Push implements Pusher interface
func (w *preloadPusherRW) Push(target string, opts *http.PushOptions) error {
	if w, ok := w.ResponseWriter.(http.Pusher); ok {
		return w.Push(target, opts)
	}
	return http.ErrNotSupported
}

// Flush implements Flusher interface
func (w *preloadPusherRW) Flush() {
	if w, ok := w.ResponseWriter.(http.Flusher); ok {
		w.Flush()
	}
}

// Hijack implements Hijacker interface
func (w *preloadPusherRW) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w, ok := w.ResponseWriter.(http.Hijacker); ok {
		return w.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
