package healthz_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet"
	. "github.com/moonrhythm/parapet/pkg/healthz"
)

func TestHealthz(t *testing.T) {
	t.Parallel()

	m := New()
	s := parapet.Server{
		Addr:               "127.0.0.1:10100",
		WaitBeforeShutdown: 200 * time.Millisecond,
		Handler:            http.NotFoundHandler(),
	}
	defer s.Shutdown()
	s.Use(m)
	go s.ListenAndServe()
	time.Sleep(50 * time.Millisecond)

	// not found
	resp, err := http.Get("http://127.0.0.1:10100")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusNotFound, resp.StatusCode)
	}

	// with host
	req, _ := http.NewRequest("GET", "http://127.0.0.1:10100/healthz", nil)
	req.Host = "localhost"
	resp, err = http.DefaultClient.Do(req)
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusNotFound, resp.StatusCode)
	}

	// liveness
	resp, err = http.Get("http://127.0.0.1:10100/healthz")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusOK, resp.StatusCode)
	}

	// liveness false
	m.Set(false)
	resp, err = http.Get("http://127.0.0.1:10100/healthz")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusServiceUnavailable, resp.StatusCode)
	}
	m.Set(true)

	// readiness
	resp, err = http.Get("http://127.0.0.1:10100/healthz?ready=1")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusOK, resp.StatusCode)
	}

	// readiness false
	m.SetReady(false)
	resp, err = http.Get("http://127.0.0.1:10100/healthz?ready=1")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusServiceUnavailable, resp.StatusCode)
	}
	m.SetReady(true)

	go s.Shutdown()
	time.Sleep(20 * time.Millisecond)

	// liveness while shutdown
	resp, err = http.Get("http://127.0.0.1:10100/healthz")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusOK, resp.StatusCode)
	}

	// readiness while shutdown
	resp, err = http.Get("http://127.0.0.1:10100/healthz?ready=1")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusServiceUnavailable, resp.StatusCode)
	}
}
