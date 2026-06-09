package h2push_test

import (
	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/h2push"
)

// Eagerly HTTP/2-push a single fixed asset on every request, before the
// downstream handler runs. The link is only pushed when the connection is
// HTTP/2 (the ResponseWriter is an http.Pusher); on HTTP/1.x it is a no-op.
func ExamplePush() {
	s := parapet.New()
	s.Use(h2push.Push("/static/app.js"))
	// s.Use(upstream.SingleHost("10.0.0.1:8080")) — the handler whose responses
	// are served alongside the pushed asset.
}

// Push assets driven by the upstream's own "Link: <...>; rel=preload" response
// headers instead of a fixed link. Each preload link is pushed as the response
// headers are written, except those marked with the "nopush" parameter.
func ExamplePreload() {
	s := parapet.New()
	s.Use(h2push.Preload())
	// A downstream handler/upstream that emits e.g.
	//   Link: </static/app.css>; rel=preload; as=style
	//   Link: </static/hero.png>; rel=preload; as=image; nopush
	// gets app.css pushed; hero.png is honored as nopush and skipped.
}
