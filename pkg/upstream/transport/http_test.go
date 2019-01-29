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

func TestHTTP(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Nil(t, r.TLS)
		assert.Equal(t, "example.com", r.Host)
		w.WriteHeader(201)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	tr := HTTP{}
	r := httptest.NewRequest("GET", "https://example.com", nil)
	r.URL.Host = strings.TrimPrefix(ts.URL, "http://")
	resp, err := tr.RoundTrip(r)
	assert.NoError(t, err)
	if assert.NotNil(t, resp) {
		assert.Equal(t, 201, resp.StatusCode)
		body, _ := ioutil.ReadAll(resp.Body)
		assert.Equal(t, "ok", string(body))
	}
}
