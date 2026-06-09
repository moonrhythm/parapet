package logger_test

import (
	"bytes"
	"net/http"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/logger"
)

// Emit a structured JSON access log line per request to stdout. Each entry
// carries timing, status, sizes and the client IP derived from the proxy
// headers; empty/zero fields are dropped from the line.
func ExampleStdout() {
	s := parapet.New()
	s.Use(logger.Stdout())
	// s.Use(upstream.SingleHost("10.0.0.1:8080")) — the handler whose
	// requests get logged.
}

// Send the access log to a custom destination instead of stdout/stderr — here
// an in-memory buffer (any io.Writer works, e.g. a file or a log shipper).
func ExampleLogger() {
	var buf bytes.Buffer

	s := parapet.New()
	s.Use(&logger.Logger{
		Writer: &buf,
	})
}

// Enrich the log record for the current request from a downstream handler by
// setting an extra field on the request context; it is encoded alongside the
// built-in fields when the request completes.
func ExampleSet() {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Set(r.Context(), "userId", "u-12345")
		w.WriteHeader(http.StatusNoContent)
	})
	_ = h
}

// Suppress the access log for matched requests — useful behind a health-check
// or other high-volume, low-value endpoint to keep logs quiet.
func ExampleDisable() {
	s := parapet.New()
	s.Use(logger.Stdout())
	s.Use(logger.Disable()) // requests reaching here are not logged
}
