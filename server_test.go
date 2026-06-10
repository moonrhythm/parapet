package parapet_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet"
)

func TestServer(t *testing.T) {
	t.Parallel()

	var called bool
	srv := &Server{}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})
	// Pre-bind an ephemeral listener and Serve it: removes the
	// bind-vs-Get race (sleep was the only sync) and the fixed-port
	// collision; once Listen returns the kernel backlog queues the dial.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if !assert.NoError(t, err) {
		return
	}
	defer ln.Close()
	srv.Addr = ln.Addr().String()
	go srv.Serve(ln)

	resp, err := http.Get("http://" + srv.Addr)
	if assert.NoError(t, err) {
		assert.Equal(t, 200, resp.StatusCode)
		assert.True(t, called)
		assert.NoError(t, srv.Shutdown())
	}
}

func TestListenAndServe(t *testing.T) {
	t.Parallel()

	// TestServer/TestServerTLS pre-bind and call Serve, so this is the ONE test
	// exercising the ListenAndServe path (addr resolve, ListenConfig bind, the
	// modifyConnListener decision). Addr :0 keeps it collision-free, and the
	// BaseContext hook — invoked with the BOUND listener before accepting —
	// delivers the real address without sleeping; errCh turns an early bind
	// failure into a fast, explicit failure instead of a deadlock.
	srv := &Server{}
	srv.Addr = "127.0.0.1:0"
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	addrCh := make(chan string, 1)
	srv.BaseContext = func(l net.Listener) context.Context {
		addrCh <- l.Addr().String()
		return context.Background()
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	var addr string
	select {
	case addr = <-addrCh:
	case err := <-errCh:
		t.Fatalf("ListenAndServe failed before binding: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("ListenAndServe never bound a listener")
	}
	resp, err := http.Get("http://" + addr)
	if assert.NoError(t, err) {
		assert.Equal(t, 200, resp.StatusCode)
		resp.Body.Close()
	}
	assert.NoError(t, srv.Shutdown())
}

func TestServerTLS(t *testing.T) {
	t.Parallel()

	var called bool
	srv := &Server{}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		assert.NotNil(t, r.TLS)
		assert.Equal(t, "https", r.Header.Get("X-Forwarded-Proto"))
		w.WriteHeader(200)
	})
	cert, err := GenerateSelfSignCertificate(SelfSign{
		CommonName: "localhost",
		Hosts:      []string{"localhost"},
	})
	assert.NoError(t, err)
	srv.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
	}
	// Same de-flake as TestServer: pre-bind ephemeral, Serve the bound
	// listener (Serve routes through ServeTLS when TLSConfig is set).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if !assert.NoError(t, err) {
		return
	}
	defer ln.Close()
	srv.Addr = ln.Addr().String()
	go srv.Serve(ln)

	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := client.Get("https://" + srv.Addr)
	if assert.NoError(t, err) {
		assert.Equal(t, 200, resp.StatusCode)
		assert.True(t, called)
		assert.NoError(t, srv.Shutdown())
	}
}
