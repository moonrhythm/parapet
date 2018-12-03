package logger

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// Logger middleware
type Logger struct {
	Writer io.Writer

	RequestID      string
	ForwardedFor   string
	ForwardedProto string
}

// Stdout creates new stdout logger
func Stdout() *Logger {
	return &Logger{
		Writer: os.Stdout,
	}
}

// Stderr creates new stderr logger
func Stderr() *Logger {
	return &Logger{
		Writer: os.Stderr,
	}
}

// ServeHandler implements middleware interface
func (m *Logger) ServeHandler(h http.Handler) http.Handler {
	if m.Writer == nil {
		m.Writer = os.Stdout
	}

	if m.RequestID == "" {
		m.RequestID = "X-Request-Id"
	}
	if m.ForwardedFor == "" {
		m.ForwardedFor = "X-Forwarded-For"
	}
	if m.ForwardedProto == "" {
		m.ForwardedProto = "X-Forwarded-Proto"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var d record
		d.Method = r.Method
		d.Host = r.Host
		d.URI = r.RequestURI
		d.UserAgent = r.UserAgent()
		d.Referer = r.Referer()
		d.RemoteIP, _, _ = net.SplitHostPort(r.RemoteAddr)
		d.ForwardedFor = r.Header.Get(m.ForwardedFor)
		d.ForwardedProto = r.Header.Get(m.ForwardedProto)
		d.ContentLength = r.ContentLength
		d.RequestID = r.Header.Get(m.RequestID)

		start := time.Now()
		nw := responseWriter{ResponseWriter: w}
		defer func() {
			if d.disable {
				return
			}

			duration := time.Since(start)
			d.Date = start.Format(time.RFC3339)
			d.Duration = duration.Nanoseconds()
			d.DurationHuman = duration.String()
			d.StatusCode = nw.statusCode
			d.ResponseBodyBytes = nw.length

			json.NewEncoder(m.Writer).Encode(&d)
		}()

		ctx := r.Context()
		ctx = context.WithValue(ctx, ctxKeyRecord{}, &d)
		r = r.WithContext(ctx)
		h.ServeHTTP(&nw, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	statusCode  int
	length      int64
}

func (w *responseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	n, err := w.ResponseWriter.Write(p)
	w.length += int64(n)
	return n, err
}
