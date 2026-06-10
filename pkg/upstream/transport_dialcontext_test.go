package upstream

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// recorderDialer is a test dial func that records whether it was invoked and
// the address it was asked to dial, then delegates to a real net.Dialer so the
// request actually succeeds. Synchronization is via atomics so it is safe to
// assert on under -race without sleeping.
type recorderDialer struct {
	called atomic.Bool
	addr   atomic.Pointer[string]
}

func (d *recorderDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d.called.Store(true)
	a := addr
	d.addr.Store(&a)
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

func (d *recorderDialer) lastAddr() string {
	p := d.addr.Load()
	if p == nil {
		return ""
	}
	return *p
}

// errDialContext returns a sentinel error without ever touching the network, to
// assert the seam is actually wired (the error must surface to the caller).
var errSentinelDial = errors.New("sentinel dial error")

func sentinelDialContext(_ context.Context, _, _ string) (net.Conn, error) {
	return nil, errSentinelDial
}

func TestHTTPTransport_DialContext(t *testing.T) {
	t.Parallel()

	t.Run("custom dialer invoked", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			w.Write([]byte("ok"))
		}))
		defer ts.Close()

		rec := &recorderDialer{}
		tr := HTTPTransport{DialContext: rec.DialContext}

		host := strings.TrimPrefix(ts.URL, "http://")
		r := httptest.NewRequest("GET", "https://example.com", nil)
		r.URL.Host = host
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		assert.True(t, rec.called.Load(), "custom DialContext must be invoked")
		assert.Equal(t, host, rec.lastAddr())
	})

	t.Run("sentinel error surfaces", func(t *testing.T) {
		t.Parallel()

		tr := HTTPTransport{DialContext: sentinelDialContext}
		r := httptest.NewRequest("GET", "https://example.com", nil)
		r.URL.Host = "10.255.255.1:80"
		_, err := tr.RoundTrip(r)
		assert.ErrorIs(t, err, errSentinelDial)
	})

	t.Run("nil dialer still works", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			w.Write([]byte("ok"))
		}))
		defer ts.Close()

		tr := HTTPTransport{}
		r := httptest.NewRequest("GET", "https://example.com", nil)
		r.URL.Host = strings.TrimPrefix(ts.URL, "http://")
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, "ok", string(body))
		}
	})
}

func TestHTTPSTransport_DialContext(t *testing.T) {
	t.Parallel()

	t.Run("custom dialer invoked", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			w.Write([]byte("ok"))
		}))
		defer ts.Close()

		rec := &recorderDialer{}
		tr := HTTPSTransport{DialContext: rec.DialContext}

		host := strings.TrimPrefix(ts.URL, "https://")
		r := httptest.NewRequest("GET", "http://example.com", nil)
		r.URL.Host = host
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		assert.True(t, rec.called.Load(), "custom DialContext must be invoked")
		assert.Equal(t, host, rec.lastAddr())
	})

	t.Run("sentinel error surfaces", func(t *testing.T) {
		t.Parallel()

		tr := HTTPSTransport{DialContext: sentinelDialContext}
		r := httptest.NewRequest("GET", "http://example.com", nil)
		r.URL.Host = "10.255.255.1:443"
		_, err := tr.RoundTrip(r)
		assert.ErrorIs(t, err, errSentinelDial)
	})

	t.Run("nil dialer still works", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			w.Write([]byte("ok"))
		}))
		defer ts.Close()

		tr := HTTPSTransport{}
		r := httptest.NewRequest("GET", "http://example.com", nil)
		r.URL.Host = strings.TrimPrefix(ts.URL, "https://")
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, "ok", string(body))
		}
	})
}

func TestH2CTransport_DialContext(t *testing.T) {
	t.Parallel()

	newH2CServer := func(t *testing.T) *httptest.Server {
		t.Helper()
		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "HTTP/2.0", r.Proto)
			w.WriteHeader(201)
			w.Write([]byte("ok"))
		}))
		ts.Config.Protocols = new(http.Protocols)
		ts.Config.Protocols.SetHTTP1(true)
		ts.Config.Protocols.SetUnencryptedHTTP2(true)
		ts.Start()
		return ts
	}

	t.Run("custom dialer invoked on h2c path", func(t *testing.T) {
		t.Parallel()

		ts := newH2CServer(t)
		defer ts.Close()

		rec := &recorderDialer{}
		tr := H2CTransport{DialContext: rec.DialContext}

		host := strings.TrimPrefix(ts.URL, "http://")
		r := httptest.NewRequest("GET", "https://example.com", nil)
		r.URL.Host = host
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		assert.True(t, rec.called.Load(), "custom DialContext must be invoked on the h2c dial path")
		assert.Equal(t, host, rec.lastAddr())
	})

	t.Run("custom dialer invoked on h1 upgrade fallback", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "HTTP/1.1", r.Proto)
			conn, rw, err := w.(http.Hijacker).Hijack()
			assert.NoError(t, err)
			rw.WriteString("HTTP/1.1 200 OK\r\n")
			rw.WriteString("Content-Length: 2\r\n")
			rw.WriteString("\r\n")
			rw.WriteString("ok")
			rw.Flush()
			conn.Close()
		}))
		ts.Config.Protocols = new(http.Protocols)
		ts.Config.Protocols.SetHTTP1(true)
		ts.Config.Protocols.SetUnencryptedHTTP2(true)
		ts.Start()
		defer ts.Close()

		rec := &recorderDialer{}
		tr := H2CTransport{DialContext: rec.DialContext}

		host := strings.TrimPrefix(ts.URL, "http://")
		r := httptest.NewRequest("GET", "https://example.com", nil)
		r.URL.Host = host
		r.Header.Set("Upgrade", "proto")
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, "ok", string(body))
		}
		assert.True(t, rec.called.Load(), "custom DialContext must be invoked on the h1 fallback dial path")
		assert.Equal(t, host, rec.lastAddr())
	})

	t.Run("sentinel error surfaces on h2c path", func(t *testing.T) {
		t.Parallel()

		tr := H2CTransport{DialContext: sentinelDialContext}
		r := httptest.NewRequest("GET", "https://example.com", nil)
		r.URL.Host = "10.255.255.1:80"
		_, err := tr.RoundTrip(r)
		assert.ErrorIs(t, err, errSentinelDial)
	})

	t.Run("nil dialer still works", func(t *testing.T) {
		t.Parallel()

		ts := newH2CServer(t)
		defer ts.Close()

		tr := H2CTransport{}
		r := httptest.NewRequest("GET", "https://example.com", nil)
		r.URL.Host = strings.TrimPrefix(ts.URL, "http://")
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, "ok", string(body))
		}
	})
}

func TestTransport_DialContext(t *testing.T) {
	t.Parallel()

	t.Run("custom dialer invoked on http path", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			w.Write([]byte("ok"))
		}))
		defer ts.Close()

		rec := &recorderDialer{}
		tr := Transport{DialContext: rec.DialContext}

		r := httptest.NewRequest("GET", ts.URL, nil)
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		assert.True(t, rec.called.Load(), "custom DialContext must be invoked on the http path")
		assert.Equal(t, strings.TrimPrefix(ts.URL, "http://"), rec.lastAddr())
	})

	t.Run("custom dialer invoked on h2c path", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "HTTP/2.0", r.Proto)
			w.WriteHeader(201)
			w.Write([]byte("ok"))
		}))
		ts.Config.Protocols = new(http.Protocols)
		ts.Config.Protocols.SetHTTP1(true)
		ts.Config.Protocols.SetUnencryptedHTTP2(true)
		ts.Start()
		defer ts.Close()

		rec := &recorderDialer{}
		tr := Transport{DialContext: rec.DialContext}

		host := strings.TrimPrefix(ts.URL, "http://")
		r := httptest.NewRequest("GET", "h2c://"+host, nil)
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		assert.True(t, rec.called.Load(), "custom DialContext must be invoked on the h2c dial path")
		assert.Equal(t, host, rec.lastAddr())
	})

	t.Run("sentinel error surfaces on h2c path", func(t *testing.T) {
		t.Parallel()

		tr := Transport{DialContext: sentinelDialContext}
		r := httptest.NewRequest("GET", "h2c://10.255.255.1:80", nil)
		_, err := tr.RoundTrip(r)
		assert.ErrorIs(t, err, errSentinelDial)
	})

	t.Run("nil dialer still works", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			w.Write([]byte("ok"))
		}))
		defer ts.Close()

		tr := Transport{}
		r := httptest.NewRequest("GET", ts.URL, nil)
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, "ok", string(body))
		}
	})
}
