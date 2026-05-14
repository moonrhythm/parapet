package timeout_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/timeout"
)

func TestTimeoutZeroDisables(t *testing.T) {
	t.Parallel()

	m := New(0)
	called := false
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestTimeoutPasses(t *testing.T) {
	t.Parallel()

	m := New(time.Second)
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "yes")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("hello"))
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "yes", w.Header().Get("X-Custom"))
	body, _ := io.ReadAll(w.Body)
	assert.Equal(t, "hello", string(body))
}

func TestTimeoutFires(t *testing.T) {
	t.Parallel()

	m := New(20 * time.Millisecond)
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		// after the timeout has fired, these writes must be swallowed.
		// Calling WriteHeader/Write also synchronizes with the timeout goroutine
		// via timeoutRW.mu, so the test can safely observe the recorder afterwards.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("should be discarded"))
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	assert.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusGatewayTimeout, resp.StatusCode)
	assert.Contains(t, string(body), "Gateway Timeout")
	assert.NotContains(t, string(body), "should be discarded")
}

func TestTimeoutCustomHandler(t *testing.T) {
	t.Parallel()

	custom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("custom"))
	})
	m := &Timout{Timeout: 20 * time.Millisecond, TimeoutHandler: custom}

	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		// touch the timeoutRW to synchronize with the timeout goroutine
		w.WriteHeader(http.StatusOK)
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	assert.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "custom", string(body))
}

func TestTimeoutHeadersCopied(t *testing.T) {
	t.Parallel()

	m := New(time.Second)
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Foo", "bar")
		w.Header().Set("X-Baz", "qux")
		w.WriteHeader(http.StatusCreated)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "bar", w.Header().Get("X-Foo"))
	assert.Equal(t, "qux", w.Header().Get("X-Baz"))
}

func TestTimeoutWriteWithoutExplicitHeader(t *testing.T) {
	t.Parallel()

	m := New(time.Second)
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("body"))
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	body, _ := io.ReadAll(w.Body)
	assert.Equal(t, "body", string(body))
}
