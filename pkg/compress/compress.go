package compress

import (
	"bufio"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/moonrhythm/parapet/pkg/header"
)

// Compress is the compress middleware
//
//nolint:govet
type Compress struct {
	New       func() Compressor
	Encoding  string // http Accept-Encoding, Content-Encoding value
	Vary      bool   // add Vary: Accept-Encoding
	Types     string // only compress for given types, * for all types
	MinLength int    // skip if Content-Length less than given value
}

// default values
const (
	defaultCompressVary      = true
	defaultCompressTypes     = "application/xml+rss application/atom+xml application/javascript application/x-javascript application/json application/rss+xml application/vnd.ms-fontobject application/x-font-ttf application/x-web-app-manifest+json application/xhtml+xml application/xml font/opentype image/svg+xml image/x-icon text/css text/html text/javascript text/plain text/x-component"
	defaultCompressMinLength = 860
)

// ServeHandler implements middleware interface
func (m Compress) ServeHandler(h http.Handler) http.Handler {
	mapTypes := make(map[string]struct{})
	for _, t := range strings.Split(m.Types, " ") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		mapTypes[t] = struct{}{}
	}

	pool := &sync.Pool{
		New: func() any {
			return m.New()
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// skip if client not support
		if !strings.Contains(header.Get(r.Header, header.AcceptEncoding), m.Encoding) {
			h.ServeHTTP(w, r)
			return
		}

		// skip if web socket
		if header.Exists(r.Header, header.SecWebsocketKey) {
			h.ServeHTTP(w, r)
			return
		}

		hh := w.Header()

		// skip if already encode
		if header.Exists(hh, header.ContentEncoding) {
			h.ServeHTTP(w, r)
			return
		}

		if m.Vary {
			header.AddIfNotExists(hh, header.Vary, header.AcceptEncoding)
		}

		cw := &compressWriter{
			ResponseWriter: w,
			pool:           pool,
			encoding:       m.Encoding,
			types:          mapTypes,
			minLength:      m.MinLength,
		}
		defer cw.Close()

		h.ServeHTTP(cw, r)
	})
}

// Compressor type
type Compressor interface {
	io.Writer
	io.Closer
	Reset(io.Writer)
	Flush() error
}

type compressWriter struct {
	http.ResponseWriter

	pool        *sync.Pool
	types       map[string]struct{}
	encoder     Compressor
	encoding    string
	minLength   int
	wroteHeader bool
}

func (w *compressWriter) init() {
	h := w.Header()

	// skip if already encode
	if header.Exists(h, header.ContentEncoding) {
		return
	}

	// skip if length < min length
	if w.minLength > 0 {
		if sl := header.Get(h, header.ContentLength); sl != "" {
			l, _ := strconv.Atoi(sl)
			if l > 0 && l < w.minLength {
				return
			}
		}
	}

	// skip if no match type
	if _, ok := w.types["*"]; !ok {
		ct, _, err := mime.ParseMediaType(header.Get(h, header.ContentType))
		if err != nil {
			ct = "application/octet-stream"
		}
		if _, ok := w.types[ct]; !ok {
			return
		}
	}

	w.encoder = w.pool.Get().(Compressor)
	w.encoder.Reset(w.ResponseWriter)
	header.Del(h, header.ContentLength)
	header.Set(h, header.ContentEncoding, w.encoding)
}

func (w *compressWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.encoder != nil {
		return w.encoder.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *compressWriter) Close() {
	if w.encoder == nil {
		return
	}
	w.encoder.Close()
	w.pool.Put(w.encoder)
}

func (w *compressWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.init()
	w.ResponseWriter.WriteHeader(code)
}

func (w *compressWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Push implements Pusher interface
func (w *compressWriter) Push(target string, opts *http.PushOptions) error {
	if w, ok := w.ResponseWriter.(http.Pusher); ok {
		return w.Push(target, opts)
	}
	return http.ErrNotSupported
}

// Flush implements Flusher interface
func (w *compressWriter) Flush() {
	if w.encoder != nil {
		w.encoder.Flush()
	}
	if w, ok := w.ResponseWriter.(http.Flusher); ok {
		w.Flush()
	}
}

// Hijack implements Hijacker interface
func (w *compressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w, ok := w.ResponseWriter.(http.Hijacker); ok {
		return w.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
