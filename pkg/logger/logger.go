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
	Writer    io.Writer
	OmitEmpty bool
}

// Stdout creates new stdout logger
func Stdout() *Logger {
	return &Logger{
		Writer:    os.Stdout,
		OmitEmpty: true,
	}
}

// Stderr creates new stderr logger
func Stderr() *Logger {
	return &Logger{
		Writer:    os.Stderr,
		OmitEmpty: true,
	}
}

// ServeHandler implements middleware interface
func (m Logger) ServeHandler(h http.Handler) http.Handler {
	if m.Writer == nil {
		m.Writer = os.Stdout
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		proto := r.Header.Get("X-Forwarded-Proto")
		realIP := r.Header.Get("X-Real-Ip")
		xff := r.Header.Get("X-Forwarded-For")
		remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)

		d := newRecord()
		d.Set("timestamp", start.Format(time.RFC3339))
		d.Set("host", r.Host)
		d.Set("requestMethod", r.Method)
		d.Set("requestUrl", proto+"://"+r.Host+r.RequestURI)
		d.Set("requestBodySize", r.ContentLength)
		d.Set("referer", r.Referer())
		d.Set("userAgent", r.UserAgent())
		d.Set("remoteIp", remoteIP)
		d.Set("realIp", realIP)
		d.Set("forwardedFor", xff)

		ctx := r.Context()
		ctx = context.WithValue(ctx, ctxKeyRecord{}, d)

		nw := responseWriter{ResponseWriter: w}
		defer func() {
			if d.disable {
				return
			}

			now := time.Now()
			duration := now.Sub(start)
			status := nw.statusCode
			if status == 0 && ctx.Err() == context.Canceled {
				status = 499
			}

			d.Set("duration", duration.Nanoseconds())
			d.Set("durationHuman", duration.String())
			d.Set("status", status)
			d.Set("responseBodySize", nw.length)

			if !nw.wroteHeaderAt.IsZero() {
				durationHeader := now.Sub(nw.wroteHeaderAt)
				d.Set("durationHeader", durationHeader.Nanoseconds())
				d.Set("durationHeaderHuman", durationHeader.String())
			}

			d.omitEmpty()
			json.NewEncoder(m.Writer).Encode(d.data)
		}()
		h.ServeHTTP(&nw, r.WithContext(ctx))
	})
}

type responseWriter struct {
	http.ResponseWriter
	wroteHeader   bool
	wroteHeaderAt time.Time
	statusCode    int
	length        int64
}

func (w *responseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.wroteHeaderAt = time.Now()
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

func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
