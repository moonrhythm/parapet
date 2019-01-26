package location_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet"
	. "github.com/moonrhythm/parapet/pkg/location"
)

func TestRegExpMatcher(t *testing.T) {
	t.Parallel()

	cases := []struct {
		Name    string
		Pattern string
		Path    string
		Matched bool
	}{
		{"Matched", `^/path/\d+$`, "/path/123", true},
		{"Catch-all Matched", "", "/path", true},
		{"Unmatched", `^/path/\d+$`, "/path2", false},
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			m := RegExp(c.Pattern)
			r := httptest.NewRequest("GET", c.Path, nil)
			w := httptest.NewRecorder()

			if c.Matched {
				called := false
				m.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						called = true
					})
				}))
				m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Fail(t, "should not be called")
				})).ServeHTTP(w, r)
				assert.True(t, called)
			} else {
				called := false
				m.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						assert.Fail(t, "should not be called")
					})
				}))
				m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
				})).ServeHTTP(w, r)
				assert.True(t, called)
			}
		})
	}
}
