package body

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"github.com/moonrhythm/parapet/pkg/internal/pool"
	"github.com/moonrhythm/parapet/pkg/logger"
)

// BufferRequest creates new request bufferer
func BufferRequest() *RequestBufferer {
	return &RequestBufferer{}
}

// RequestBufferer reads entire request body before send to next middleware
type RequestBufferer struct{}

// ServeHandler implements middleware interface
func (m RequestBufferer) ServeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// case 1: empty body
		if r.ContentLength == 0 {
			h.ServeHTTP(w, r)
			return
		}

		// case 2: known body size and can fit in buffer
		if r.ContentLength > 0 && r.ContentLength <= pool.Size() {
			b := pool.Get()
			defer pool.Put(b)

			n, err := io.ReadAtLeast(r.Body, b, int(r.ContentLength))
			if err == io.EOF {
				err = nil
			}
			if err != nil {
				// client close connection
				return
			}

			r.Body.Close()
			r.Body = ioutil.NopCloser(bytes.NewReader(b[:n]))

			if r.Context().Err() == context.Canceled {
				return
			}

			h.ServeHTTP(w, r)
			return
		}

		// case 3: body larger than buffer size or unknown size, then buffer to file
		fp, err := ioutil.TempFile("", "request-*")
		if err != nil {
			log.Println("can not create temp file;", err)

			// fallback to send body directly to upstream
			h.ServeHTTP(w, r)
			return
		}
		defer func() {
			fp.Close()
			os.Remove(fp.Name())
		}()

		b := pool.Get()
		_, err = io.CopyBuffer(fp, r.Body, b)
		pool.Put(b)
		if err == io.EOF {
			err = nil
		}
		if err != nil {
			// client may close connection,
			// or send invalid transfer encoding content
			return
		}
		r.Body.Close()

		r.ContentLength, _ = fp.Seek(0, io.SeekCurrent)
		r.TransferEncoding = []string{} // change to identity encoding
		r.Body = fp
		logger.Set(r.Context(), "requestBodySize", r.ContentLength)

		fp.Seek(0, io.SeekStart)

		if r.Context().Err() == context.Canceled {
			return
		}

		h.ServeHTTP(w, r)
	})
}
