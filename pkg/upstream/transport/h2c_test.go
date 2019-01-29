package transport_test

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	. "github.com/moonrhythm/parapet/pkg/upstream/transport"
)

func TestH2C(t *testing.T) {
	t.Parallel()

	pri := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PRI" {
			pri = true
		}
		h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Nil(t, r.TLS)
			assert.Equal(t, "example.com", r.Host)
			w.WriteHeader(201)
			w.Write([]byte("ok"))
		}), &http2.Server{}).ServeHTTP(w, r)
	}))
	defer ts.Close()

	tr := H2C{}
	r := httptest.NewRequest("GET", "https://example.com", nil)
	r.URL.Host = strings.TrimPrefix(ts.URL, "http://")
	resp, err := tr.RoundTrip(r)
	assert.NoError(t, err)
	if assert.NotNil(t, resp) {
		assert.Equal(t, 201, resp.StatusCode)
		body, _ := ioutil.ReadAll(resp.Body)
		assert.Equal(t, "ok", string(body))
	}
	assert.True(t, pri)
}
