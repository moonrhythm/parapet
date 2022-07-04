package parapet_test

import (
	"crypto/tls"
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
	srv.Addr = "127.0.0.1:8081"
	go srv.ListenAndServe()
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:8081")
	if assert.NoError(t, err) {
		assert.Equal(t, 200, resp.StatusCode)
		assert.True(t, called)
		assert.NoError(t, srv.Shutdown())
	}
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
	srv.Addr = "127.0.0.1:8082"
	cert, err := GenerateSelfSignCertificate(SelfSign{
		CommonName: "localhost",
		Hosts:      []string{"localhost"},
	})
	assert.NoError(t, err)
	srv.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
	}
	go srv.ListenAndServe()
	time.Sleep(100 * time.Millisecond)

	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := client.Get("https://localhost:8082")
	if assert.NoError(t, err) {
		assert.Equal(t, 200, resp.StatusCode)
		assert.True(t, called)
		assert.NoError(t, srv.Shutdown())
	}
}
