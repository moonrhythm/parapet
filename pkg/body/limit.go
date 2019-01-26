package body

import (
	"context"
	"io"
	"net/http"
)

// LimitRequest creates new request limiter
func LimitRequest(size int64) RequestLimiter {
	return RequestLimiter{Size: size}
}

// RequestLimiter limits request body size
type RequestLimiter struct {
	Size           int64
	LimitedHandler http.Handler
}

// ServeHandler implements middleware interface
func (m RequestLimiter) ServeHandler(h http.Handler) http.Handler {
	if m.Size < 0 {
		// unlimited
		return h
	}

	if m.LimitedHandler == nil {
		m.LimitedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// case 1: known body size
		if r.ContentLength >= 0 {
			if r.ContentLength > m.Size {
				m.LimitedHandler.ServeHTTP(w, r)
				return
			}

			h.ServeHTTP(w, r)
			return
		}

		// case 2: chunked transfer encoding, unknown content length
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		body := r.Body

		k := m.Size
		r.Body = &readCloser{
			readerFunc: func(p []byte) (n int, err error) {
				if k <= 0 {
					err = io.EOF

					m.LimitedHandler.ServeHTTP(w, r)

					cancel() // prevent upstream send body to client
					return
				}

				if int64(len(p)) > k {
					p = p[:k]
				}
				n, err = body.Read(p)
				k -= int64(n)
				return
			},
			Closer: body,
		}

		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

type readCloser struct {
	readerFunc func(p []byte) (int, error)
	io.Closer
}

func (r *readCloser) Read(p []byte) (int, error) {
	return r.readerFunc(p)
}
