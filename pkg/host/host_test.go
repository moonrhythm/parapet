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
		{"Empty", nil, "localhost", true},
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
