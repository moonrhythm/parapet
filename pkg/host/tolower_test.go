package host_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/host"
)

func TestToLower(t *testing.T) {
	t.Parallel()

	cases := []struct {
		Host   string
		Result string
	}{
		{"host", "host"},
		{"HOST.local:443", "host.local:443"},
		{"EXAMPLE.COM", "example.com"},
	}

	for _, tC := range cases {
		t.Run(tC.Host, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Host = tC.Host
			w := httptest.NewRecorder()
			called := false
			ToLower().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tC.Result, r.Host)
				called = true
			})).ServeHTTP(w, r)
			assert.True(t, called)
		})
	}
}
