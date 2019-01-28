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
		Addr:               ":10100",
		WaitBeforeShutdown: 200 * time.Millisecond,
	}
	defer s.Shutdown()
	s.Use(m)
	go s.ListenAndServe()
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:10100")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusOK, resp.StatusCode)
	}

	go s.Shutdown()
	time.Sleep(20 * time.Millisecond)
	resp, err = http.Get("http://127.0.0.1:10100")
	if assert.NoError(t, err) {
		assert.EqualValues(t, http.StatusServiceUnavailable, resp.StatusCode)
	}
}
