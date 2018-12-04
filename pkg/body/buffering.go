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
func (m *RequestBufferer) ServeHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.ContentLength == 0:
			// empty body
		case r.ContentLength != -1 && r.ContentLength <= pool.Size():
			// known body size and can fit in buffer
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
		default:
			// body larger than buffer size or unknown size,
			// then buffer to file
			fp, err := ioutil.TempFile("", "request-*")
			if err != nil {
				log.Println("can not create temp file;", err)

				// fallback to send body directly to upstream
				break
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

			r.ContentLength, _ = fp.Seek(0, os.SEEK_CUR)
			r.TransferEncoding = []string{} // change to identity encoding
			r.Body = fp
			logger.Set(r.Context(), "content_length", r.ContentLength)

			fp.Seek(0, os.SEEK_SET)
		}

		if r.Context().Err() == context.Canceled {
			return
		}

		h.ServeHTTP(w, r)
	})
}
