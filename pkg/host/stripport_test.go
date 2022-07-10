package host_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/host"
)

func TestStripPort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		Host   string
		Result string
	}{
		{"host:8080", "host"},
		{"host.local:443", "host.local"},
		{"example.com", "example.com"},
	}

	for _, tC := range cases {
		t.Run(tC.Host, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Host = "host:8080"
			w := httptest.NewRecorder()
			called := false
			StripPort().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "host", r.Host)
				called = true
			})).ServeHTTP(w, r)
			assert.True(t, called)
		})
	}
}
