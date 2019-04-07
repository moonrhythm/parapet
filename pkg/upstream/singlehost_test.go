package upstream_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/upstream"
)

func TestSingleHost(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "example.com"
	w := httptest.NewRecorder()

	called := false
	tr := &mockTransport{
		roundTripFunc: func(r *http.Request) (*http.Response, error) {
			w := httptest.NewRecorder()
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
			called = true
			assert.Equal(t, "/", r.URL.Path)
			assert.Equal(t, "fakehost", r.URL.Host)
			assert.Equal(t, "example.com", r.Host)
			return w.Result(), nil
		},
	}
	SingleHost("fakehost", tr).ServeHandler(nil).ServeHTTP(w, r)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}
