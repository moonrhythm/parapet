package host_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet"
	. "github.com/moonrhythm/parapet/pkg/host"
)

func TestCIDR(t *testing.T) {
	t.Parallel()

	cases := []struct {
		Name    string
		Hosts   []string
		Host    string
		Matched bool
	}{
		{"Empty CIDR should not matched", nil, "127.0.0.1", false},
		{"0.0.0.0/0", []string{"0.0.0.0/0"}, "10.0.1.1", true},
		{"Exact", []string{"10.0.1.1/32"}, "10.0.1.1", true},
		{"2rd Exact", []string{"10.1.2.3/32", "10.0.1.1/32"}, "10.0.1.1", true},
		{"2rd Match", []string{"10.55.55.55/32", "10.0.0.0/8"}, "10.0.0.2", true},
		{"Not Matched", []string{"10.0.1.1/32"}, "10.0.1.2", false},
		{"Not Matched Hostname", []string{"10.0.1.1/32"}, "localhost", false},
		{"Invalid CIDR", []string{"10.0.1.1"}, "10.0.1.1", false},
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			m := NewCIDR(c.Hosts...)

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
