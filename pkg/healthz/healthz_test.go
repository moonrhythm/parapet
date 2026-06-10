package healthz_test

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet"
	. "github.com/moonrhythm/parapet/pkg/healthz"
)

func TestHealthz(t *testing.T) {
	t.Parallel()

	m := New()
	s := parapet.Server{
		// WaitBeforeShutdown must stay comfortably above the shutdown poll
		// deadline below, so the listener is guaranteed to still be
		// accepting while the post-shutdown asserts run.
		WaitBeforeShutdown: 2 * time.Second,
		Handler:            http.NotFoundHandler(),
	}
	s.Use(m)

	// bind synchronously on an ephemeral port: no sleep can reliably wait
	// for ListenAndServe's goroutine to finish the listen syscall, and a
	// hardcoded port collides across concurrently running test binaries.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go s.Serve(ln)
	baseURL := "http://" + ln.Addr().String()

	// not found
	resp, err := http.Get(baseURL)
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusNotFound, resp.StatusCode)
	}

	// with host
	req, _ := http.NewRequest("GET", baseURL+"/healthz", nil)
	req.Host = "localhost"
	resp, err = http.DefaultClient.Do(req)
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusNotFound, resp.StatusCode)
	}

	// liveness
	resp, err = http.Get(baseURL + "/healthz")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusOK, resp.StatusCode)
	}

	// liveness false
	m.Set(false)
	resp, err = http.Get(baseURL + "/healthz")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusServiceUnavailable, resp.StatusCode)
	}
	m.Set(true)

	// readiness
	resp, err = http.Get(baseURL + "/healthz?ready=1")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusOK, resp.StatusCode)
	}

	// readiness false
	m.SetReady(false)
	resp, err = http.Get(baseURL + "/healthz?ready=1")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusServiceUnavailable, resp.StatusCode)
	}
	m.SetReady(true)

	// run Shutdown exactly once and wait for it before returning, instead
	// of the old go s.Shutdown() + defer s.Shutdown() pair which shut down
	// twice and let the test return while the server was still draining.
	shutdownDone := make(chan struct{})
	go func() {
		s.Shutdown()
		close(shutdownDone)
	}()
	defer func() { <-shutdownDone }()

	// readiness while shutdown: the shutdown flag is flipped on a
	// doubly-spawned goroutine (go s.Shutdown() -> go onShutdown()), so a
	// fixed sleep races the scheduler; poll for the observable 503 instead.
	// The deadline is strictly less than WaitBeforeShutdown, so a missing
	// flag flip fails here as a timeout rather than as a confusing
	// connection-refused after the listener closes.
	assert.Eventually(t, func() bool {
		resp, err := http.Get(baseURL + "/healthz?ready=1")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode == http.StatusServiceUnavailable
	}, time.Second, 5*time.Millisecond)

	// liveness while shutdown: the poll above succeeded inside the
	// WaitBeforeShutdown window, so the listener is still accepting here.
	resp, err = http.Get(baseURL + "/healthz")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusOK, resp.StatusCode)
	}
}
