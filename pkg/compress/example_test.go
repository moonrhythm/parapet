package compress_test

import (
	"github.com/klauspost/compress/zstd"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/compress"
)

// Compress responses with gzip. The middleware negotiates with the client via
// Accept-Encoding and only kicks in for compressible content types above its
// MinLength threshold, so it's safe to mount unconditionally.
func ExampleGzip() {
	s := parapet.New()
	s.Use(compress.Gzip())
	// s.Use(upstream.SingleHost(...)) — the handler whose responses get compressed.
}

// Offer multiple encodings and let the client's Accept-Encoding pick. Each
// Compress self-skips when the client doesn't list its encoding or when the
// response is already encoded, so the INNERMOST middleware (the last Use, closest
// to the handler) compresses the response first and wins — the outer ones then
// see Content-Encoding already set and pass through. Order them least- to
// most-preferred: a client that accepts zstd gets zstd, else gzip, else deflate.
func ExampleZstd() {
	s := parapet.New()
	s.Use(compress.Deflate()) // outermost: last-resort fallback
	s.Use(compress.Gzip())
	s.Use(compress.Zstd())    // innermost: runs first on the response, so zstd wins
}

// Tune the compressor: pick a stronger zstd level and tighten which responses
// get compressed. Types is a space-separated MIME allow-list ("*" for all), and
// MinLength skips bodies whose Content-Length is below the threshold.
func ExampleZstdWithLevel() {
	m := compress.ZstdWithLevel(zstd.SpeedBetterCompression)
	m.Types = "text/html text/css application/json"
	m.MinLength = 1024 // don't bother compressing tiny payloads
	m.Vary = true      // add Vary: Accept-Encoding so caches key on it

	s := parapet.New()
	s.Use(m)
}
