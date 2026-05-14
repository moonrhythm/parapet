package parapet_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet"
)

func TestNewDefaults(t *testing.T) {
	t.Parallel()

	s := New()
	assert.Equal(t, 620*time.Second, s.IdleTimeout)
	assert.Equal(t, 3*time.Minute, s.TCPKeepAlivePeriod)
	assert.Equal(t, 30*time.Second, s.GraceTimeout)
	assert.Equal(t, 10*time.Second, s.WaitBeforeShutdown)
	assert.NotNil(t, s.TrustProxy)
	assert.NotNil(t, s.Handler)
}

func TestNewFrontendDefaults(t *testing.T) {
	t.Parallel()

	s := NewFrontend()
	assert.Equal(t, 10*time.Second, s.ReadHeaderTimeout)
	assert.Equal(t, time.Minute, s.ReadTimeout)
	assert.Equal(t, time.Minute, s.WriteTimeout)
	assert.Equal(t, 75*time.Second, s.IdleTimeout)
	assert.Nil(t, s.TrustProxy, "frontend should not trust proxy by default")
	assert.False(t, s.H2C)
}

func TestNewBackendDefaults(t *testing.T) {
	t.Parallel()

	s := NewBackend()
	assert.True(t, s.H2C)
	assert.NotNil(t, s.TrustProxy)
	assert.Equal(t, 620*time.Second, s.IdleTimeout)
}

func TestTrustedAlwaysTrue(t *testing.T) {
	t.Parallel()

	c := Trusted()
	r := httptest.NewRequest("GET", "/", nil)
	assert.True(t, c(r))
}

func TestTrustCIDRs(t *testing.T) {
	t.Parallel()

	c := TrustCIDRs([]string{"10.0.0.0/8", "192.168.1.0/24"})

	cases := []struct {
		addr string
		want bool
	}{
		{"10.1.2.3:1", true},
		{"192.168.1.5:1", true},
		{"192.168.2.5:1", false},
		{"8.8.8.8:1", false},
		{"not-an-ip", false},
		{"", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.addr
		assert.Equal(t, tc.want, c(r), "addr=%q", tc.addr)
	}
}

func TestTrustCIDRsEmpty(t *testing.T) {
	t.Parallel()

	c := TrustCIDRs(nil)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1"
	assert.False(t, c(r))
}

func TestServerUseAppliesChain(t *testing.T) {
	t.Parallel()

	s := New()
	s.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("inner"))
	})
	s.Use(MiddlewareFunc(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("["))
			next.ServeHTTP(w, r)
			_, _ = w.Write([]byte("]"))
		})
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	assert.Equal(t, "[inner]", w.Body.String())
}

func TestServerUsePanicsAfterServe(t *testing.T) {
	t.Parallel()

	s := New()
	s.Handler = http.NotFoundHandler()

	// trigger handler config
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	assert.Panics(t, func() {
		s.Use(MiddlewareFunc(func(next http.Handler) http.Handler { return next }))
	})
}

func TestServerContextKey(t *testing.T) {
	t.Parallel()

	s := New()
	s.WaitBeforeShutdown = 0
	s.GraceTimeout = 100 * time.Millisecond
	srvFromCtx := func() any { return nil }
	s.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srvFromCtx = func() any { return r.Context().Value(ServerContextKey) }
		w.WriteHeader(http.StatusOK)
	})

	// http.Server populates BaseContext on Serve(). Drive a real listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer ln.Close()
	s.Addr = ln.Addr().String()

	go s.Serve(ln)
	defer s.Shutdown()

	// wait for server to be ready
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+s.Addr, nil)
	resp, err := http.DefaultClient.Do(req)
	if assert.NoError(t, err) {
		resp.Body.Close()
	}

	assert.Same(t, s, srvFromCtx())
}
