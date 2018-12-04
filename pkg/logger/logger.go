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

	RequestID string
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		d := newRecord()
		d.Set("date", start.Format(time.RFC3339))
		d.Set("method", r.Method)
		d.Set("host", r.Host)
		d.Set("uri", r.RequestURI)
		d.Set("user_agent", r.UserAgent())
		d.Set("referer", r.Referer())
		remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)
		d.Set("remote_ip", remoteIP)
		d.Set("content_length", r.ContentLength)
		d.Set("real_ip", r.Header.Get("X-Forwarded-For"))
		d.Set("proto", r.Header.Get("X-Forwarded-Proto"))

		nw := responseWriter{ResponseWriter: w}
		defer func() {
			if d.disable {
				return
			}

			duration := time.Since(start)
			d.Set("duration", duration.Nanoseconds())
			d.Set("duration_human", duration.String())
			d.Set("status_code", nw.statusCode)
			d.Set("response_body_bytes", nw.length)

			json.NewEncoder(m.Writer).Encode(d.data)
		}()

		ctx := r.Context()
		ctx = context.WithValue(ctx, ctxKeyRecord{}, d)
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
