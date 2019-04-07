package transport_test

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/upstream/transport"
)

func TestHTTPS(t *testing.T) {
	t.Parallel()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotNil(t, r.TLS)
		assert.Equal(t, "example.com", r.Host)
		w.WriteHeader(201)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	tr := HTTPS{}
	r := httptest.NewRequest("GET", "http://example.com", nil)
	r.URL.Host = strings.TrimPrefix(ts.URL, "https://")
	resp, err := tr.RoundTrip(r)
	assert.NoError(t, err)
	if assert.NotNil(t, resp) {
		assert.Equal(t, 201, resp.StatusCode)
		body, _ := ioutil.ReadAll(resp.Body)
		assert.Equal(t, "ok", string(body))
	}
}
