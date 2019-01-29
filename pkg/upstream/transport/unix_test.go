package transport_test

import (
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/upstream/transport"
)

func TestUnix(t *testing.T) {
	t.Parallel()

	ts := http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Nil(t, r.TLS)
			assert.Equal(t, "example.com", r.Host)
			w.WriteHeader(201)
			w.Write([]byte("ok"))
		}),
	}

	fn := filepath.Join(os.TempDir(), "parapet-test-tr-unix")
	lis, err := net.Listen("unix", fn)
	if err != nil {
		assert.Fail(t, "can not create unix listener")
	}
	defer os.Remove(fn)
	defer lis.Close()
	go ts.Serve(lis)

	tr := Unix{}
	r := httptest.NewRequest("GET", "https://example.com", nil)
	r.URL.Host = fn
	resp, err := tr.RoundTrip(r)
	assert.NoError(t, err)
	if assert.NotNil(t, resp) {
		assert.Equal(t, 201, resp.StatusCode)
		body, _ := ioutil.ReadAll(resp.Body)
		assert.Equal(t, "ok", string(body))
	}
}
