package header_test

import (
	"fmt"
	"net/http"

	"github.com/moonrhythm/parapet/pkg/header"
)

// Set replaces any existing values for a key with a single value. Unlike
// http.Header.Set it does NOT canonicalize the key, so pair it with this
// package's canonical-name constants (header.ContentType, header.XRequestID, …).
func ExampleSet() {
	h := http.Header{}
	header.Set(h, header.ContentType, "application/json")
	header.Set(h, header.ContentType, "text/plain") // replaces, not appends

	fmt.Println(h.Get("Content-Type"))
	// Output: text/plain
}

// Get returns the first value for a key, or "" when the key is absent or the
// header map is nil. It reads the key verbatim — use the canonical constants.
func ExampleGet() {
	h := http.Header{}
	header.Set(h, header.XRequestID, "abc-123")

	fmt.Printf("id=%q missing=%q\n", header.Get(h, header.XRequestID), header.Get(h, header.Origin))
	// Output: id="abc-123" missing=""
}

// Add appends a value, keeping any values already present — the way you build a
// multi-valued header such as Vary.
func ExampleAdd() {
	h := http.Header{}
	header.Add(h, header.Vary, "Origin")
	header.Add(h, header.Vary, "Accept-Encoding")

	fmt.Println(h[header.Vary])
	// Output: [Origin Accept-Encoding]
}

// AddIfNotExists appends a value only when that exact value is not already
// present, so re-running it never duplicates an entry.
func ExampleAddIfNotExists() {
	h := http.Header{}
	header.AddIfNotExists(h, header.Vary, "Origin")
	header.AddIfNotExists(h, header.Vary, "Origin") // no-op, already there

	fmt.Println(h[header.Vary])
	// Output: [Origin]
}

// Exists reports whether a key is set to a non-empty value. A present-but-empty
// value counts as absent.
func ExampleExists() {
	h := http.Header{}
	header.Set(h, header.Authorization, "Bearer t0ken")

	fmt.Println(header.Exists(h, header.Authorization), header.Exists(h, header.Origin))
	// Output: true false
}

// Del removes a key. It is nil-safe — deleting from a nil header is a no-op
// rather than a panic.
func ExampleDel() {
	h := http.Header{}
	header.Set(h, header.Authorization, "Bearer t0ken")
	header.Del(h, header.Authorization)

	fmt.Println(header.Exists(h, header.Authorization))
	// Output: false
}

// SetShared assigns a pre-built value slice into the header map, sharing its
// backing array across every call instead of allocating a fresh []string per
// request. It is a hot-path optimization for response-header values that are
// fixed at construction time and treated as immutable.
func ExampleSetShared() {
	// Built once, e.g. at middleware construction.
	hsts := []string{"max-age=63072000; includeSubDomains; preload"}

	// Reused on every response without re-allocating the slice.
	resp := http.Header{}
	header.SetShared(resp, header.StrictTransportSecurity, hsts)

	fmt.Println(resp.Get("Strict-Transport-Security"))
	// Output: max-age=63072000; includeSubDomains; preload
}
