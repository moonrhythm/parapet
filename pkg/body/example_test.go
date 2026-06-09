package body_test

import (
	"net/http"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/body"
)

// Reject request bodies larger than the given size. Requests advertising a
// Content-Length over the limit are refused outright; chunked requests with no
// declared length are streamed and cut off once the limit is exceeded. A size of
// -1 disables the limit.
func ExampleLimitRequest() {
	s := parapet.New()
	s.Use(body.LimitRequest(10 << 20)) // cap request bodies at 10 MiB
}

// Replace the default "413 Request Entity Too Large" response with a custom
// handler invoked when a request exceeds the limit.
func ExampleLimitRequest_customHandler() {
	m := body.LimitRequest(1 << 20)
	m.LimitedHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upload too big, max 1 MiB", http.StatusRequestEntityTooLarge)
	})

	s := parapet.New()
	s.Use(m)
}

// Read the entire request body before forwarding it upstream. Small bodies are
// held in memory and larger ones spilled to a temp file, so the upstream always
// sees a known Content-Length. Useful for upstreams that can't handle chunked or
// slow-trickling request bodies.
func ExampleBufferRequest() {
	s := parapet.New()
	s.Use(body.BufferRequest())
}
