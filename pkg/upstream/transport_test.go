package upstream

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestH2CTransport(t *testing.T) {
	t.Parallel()

	t.Run("HTTP", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "PRI", r.Method)
			h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Nil(t, r.TLS)
				assert.Equal(t, "HTTP/2.0", r.Proto)
				assert.Equal(t, "example.com", r.Host)
				w.WriteHeader(201)
				w.Write([]byte("ok"))
			}), &http2.Server{}).ServeHTTP(w, r)
		}))
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

	t.Run("Upgrade", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.NotEqual(t, "PRI", r.Method)
			h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "proto", r.Header.Get("Upgrade"))
				assert.Equal(t, "HTTP/1.1", r.Proto)

				conn, rw, err := w.(http.Hijacker).Hijack()
				assert.NoError(t, err)
				rw.WriteString("HTTP/1.1 200 OK\r\n")
				rw.WriteString("Content-Length: 2\r\n")
				rw.WriteString("\r\n")
				rw.WriteString("ok")
				rw.Flush()
				conn.Close()
			}), &http2.Server{}).ServeHTTP(w, r)
		}))
		defer ts.Close()

		tr := H2CTransport{}
		r := httptest.NewRequest("GET", "https://example.com", nil)
		r.URL.Host = strings.TrimPrefix(ts.URL, "http://")
		r.Header.Set("Upgrade", "proto")
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, "ok", string(body))
		}
	})
}

func TestHTTPTransport(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Nil(t, r.TLS)
		assert.Equal(t, "example.com", r.Host)
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
}

func TestHTTPSTransport(t *testing.T) {
	t.Parallel()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NotNil(t, r.TLS)
		assert.Equal(t, "example.com", r.Host)
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
}

func TestUnixTransport(t *testing.T) {
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

	tr := UnixTransport{}
	r := httptest.NewRequest("GET", "https://example.com", nil)
	r.URL.Host = fn
	resp, err := tr.RoundTrip(r)
	assert.NoError(t, err)
	if assert.NotNil(t, resp) {
		assert.Equal(t, 201, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Equal(t, "ok", string(body))
	}
}

func TestTransport(t *testing.T) {
	tr := Transport{}

	t.Run("HTTP", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			w.Write([]byte("ok http"))
		}))
		defer ts.Close()

		r := httptest.NewRequest("GET", ts.URL, nil)
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, "ok http", string(body))
		}
	})

	t.Run("HTTPS", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			w.Write([]byte("ok https"))
		}))
		defer ts.Close()

		r := httptest.NewRequest("GET", ts.URL, nil)
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, "ok https", string(body))
		}
	})

	t.Run("H2C", func(t *testing.T) {
		t.Parallel()

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "PRI", r.Method)
			h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Nil(t, r.TLS)
				assert.Equal(t, "HTTP/2.0", r.Proto)
				w.WriteHeader(201)
				w.Write([]byte("ok h2c"))
			}), &http2.Server{}).ServeHTTP(w, r)
		}))
		defer ts.Close()

		r := httptest.NewRequest("GET", "h2c://"+strings.TrimPrefix(ts.URL, "http://"), nil)
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			assert.Equal(t, 201, resp.StatusCode)
			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, "ok h2c", string(body))
		}
	})

	t.Run("H2C Upgrade", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.NotEqual(t, "PRI", r.Method)
			h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "proto", r.Header.Get("Upgrade"))
				assert.Equal(t, "HTTP/1.1", r.Proto)

				conn, rw, err := w.(http.Hijacker).Hijack()
				assert.NoError(t, err)
				rw.WriteString("HTTP/1.1 200 OK\r\n")
				rw.WriteString("Content-Length: 2\r\n")
				rw.WriteString("\r\n")
				rw.WriteString("ok")
				rw.Flush()
				conn.Close()
			}), &http2.Server{}).ServeHTTP(w, r)
		}))
		defer ts.Close()

		r := httptest.NewRequest("GET", "h2c://"+strings.TrimPrefix(ts.URL, "http://"), nil)
		r.Header.Set("Upgrade", "proto")
		resp, err := tr.RoundTrip(r)
		assert.NoError(t, err)
		if assert.NotNil(t, resp) {
			body, _ := io.ReadAll(resp.Body)
			assert.Equal(t, "ok", string(body))
		}
	})
}
