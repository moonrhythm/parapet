package host_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet"
	. "github.com/moonrhythm/parapet/pkg/host"
)

func TestHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		Name    string
		Hosts   []string
		Host    string
		Matched bool
	}{
		{"Empty should not matched", nil, "localhost", false},
		{"Wildcard", []string{"*"}, "localhost", true},
		{"Exact", []string{"moonrhythm.io"}, "moonrhythm.io", true},
		{"2rd Exact", []string{"google.com", "moonrhythm.io"}, "moonrhythm.io", true},
		{"2rd Wildcard", []string{"google.com", "*"}, "moonrhythm.io", true},
		{"Prefix Wildcard", []string{"*.moonrhythm.io"}, "www.moonrhythm.io", true},
		{"Prefix nested Wildcard", []string{"*.moonrhythm.io"}, "www.test.moonrhythm.io", true},
		{"Prefix Wildcard not matched", []string{"*.moonrhythm.io"}, "moonrhythm.io", false},
		{"Prefix Wildcard not matched", []string{"*.www.moonrhythm.io"}, "aaa.www.test.moonrhythm.io", false},
		{"Not Matched", []string{"moonrhythm.io"}, "google.com", false},
		{"Edge case #1", []string{"moonrhythm.io"}, "google", false},
		{"Edge case #2", []string{"moonrhythm.io"}, ".com", false},
		{"Edge case #3", []string{"moonrhythm.io"}, ".", false},
		{"Edge case #4", []string{"moonrhythm.io"}, "", false},
		{"Case insensitive request host", []string{"moonrhythm.io"}, "MoonRhythm.IO", true},
		{"Case insensitive config host", []string{"MoonRhythm.IO"}, "moonrhythm.io", true},
		// Non-ASCII case folding: the only uppercase char is non-ASCII, so the
		// ASCII A-Z scan alone would miss it. Must still fold like the original
		// unconditional strings.ToLower.
		{"Case insensitive non-ASCII request host", []string{"éxample.io"}, "Éxample.io", true},
		{"Case insensitive non-ASCII config host", []string{"Éxample.io"}, "éxample.io", true},
		{"Strip port from request host", []string{"moonrhythm.io"}, "moonrhythm.io:8080", true},
		{"Strip trailing dot from request host", []string{"moonrhythm.io"}, "moonrhythm.io.", true},
		{"Strip port wildcard", []string{"*.moonrhythm.io"}, "www.moonrhythm.io:443", true},
		{"IPv6 with port", []string{"[::1]"}, "[::1]:8080", true},
		// Malformed bracketed-but-colonless inputs must round-trip through the
		// matcher with brackets intact, matching the pre-optimization behavior
		// (the bracket-unwrap path only fires when a port separator is present).
		{"Bracketed no colon configured matches itself", []string{"[host]"}, "[host]", true},
		{"Bracketed no colon does not strip brackets", []string{"host"}, "[host]", false},
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			m := New(c.Hosts...)

			called := false
			falled := false
			m.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					h.ServeHTTP(w, r)
				})
			}))

			r := httptest.NewRequest("GET", "/", nil)
			r.Host = c.Host
			w := httptest.NewRecorder()
			m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				falled = true
			})).ServeHTTP(w, r)
			assert.Equal(t, c.Matched, called)
			assert.NotEqual(t, c.Matched, falled)
		})
	}
}
