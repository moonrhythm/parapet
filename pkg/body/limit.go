package body

import (
	"context"
	"io"
	"io/ioutil"
	"net/http"

	"github.com/moonrhythm/parapet/pkg/internal/pool"
)

// LimitRequest creates new request limiter
func LimitRequest(size int64) *RequestLimiter {
	return &RequestLimiter{Size: size}
}

// RequestLimiter limits request body size
type RequestLimiter struct {
	Size    int64
	Handler http.Handler
}

// ServeHandler implements middleware interface
func (m *RequestLimiter) ServeHandler(h http.Handler) http.Handler {
	if m.Size < 0 {
		// unlimit
		return h
	}

	if m.Handler == nil {
		m.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// case 1: content length
		if r.ContentLength != -1 && r.ContentLength > m.Size {
			m.Handler.ServeHTTP(w, r)
			return
		}

		// case 2: chunked transfer encoding, unknown content length
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		r = r.WithContext(ctx)
		body := r.Body

		k := m.Size
		r.Body = readCloser{
			Reader: readerFunc(func(p []byte) (n int, err error) {
				if k <= 0 {
					err = io.EOF

					// already send 100 Continue, need to read all request's body
					// or TCP will conflict
					b := pool.Get()
					io.CopyBuffer(ioutil.Discard, body, b)
					pool.Put(b)

					m.Handler.ServeHTTP(w, r)

					cancel() // prevent upstream to send body to client
					return
				}

				if int64(len(p)) > k {
					p = p[0:k]
				}
				n, err = body.Read(p)
				k -= int64(n)
				return
			}),
			Closer: body,
		}

		h.ServeHTTP(w, r)
	})
}

type readCloser struct {
	io.Reader
	io.Closer
}

type readerFunc func(p []byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) {
	return f(p)
}
