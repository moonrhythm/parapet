package upstream

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/moonrhythm/parapet/pkg/logger"
)

// Errors
var (
	ErrUnavailable = errors.New("upstream: unavailable")
)

// Upstream controls request flow to upstream server via load balancer
type Upstream struct {
	Transport     http.RoundTripper
	ErrorLog      *log.Logger
	Host          string // override host
	Path          string // target prefix path
	Retries       int
	BackoffFactor time.Duration
}

// New creates new upstream
func New(transport http.RoundTripper) *Upstream {
	return &Upstream{
		Transport:     transport,
		Retries:       3,
		BackoffFactor: 50 * time.Millisecond,
	}
}

func (m *Upstream) logf(format string, v ...interface{}) {
	if m.ErrorLog == nil {
		log.Printf(format, v...)
		return
	}
	m.ErrorLog.Printf(format, v...)
}

// ServeHandler implements middleware interface
func (m Upstream) ServeHandler(h http.Handler) http.Handler {
	if m.Path == "" {
		m.Path = "/"
	}
	targetPath, err := url.ParseRequestURI(m.Path)
	if err != nil {
		panic(err)
	}

	var p http.Handler
	p = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Path = singleJoiningSlash(targetPath.Path, req.URL.Path)

			if targetPath.RawQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = targetPath.RawQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = targetPath.RawQuery + "&" + req.URL.RawQuery
			}
			if _, ok := req.Header["User-Agent"]; !ok {
				req.Header.Set("User-Agent", "")
			}

			if m.Host != "" {
				req.Host = m.Host
			}

			req.RemoteAddr = "" // disable httputil.ReverseProxy to add X-Forwarded-For since we already added
		},
		BufferPool: bytesPool,
		Transport:  &m,
		ErrorLog:   m.ErrorLog,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if err == context.Canceled {
				// client canceled request
				return
			}

			if canRetry(r) {
				ctx := r.Context()
				retry, _ := ctx.Value(retryContextKey{}).(int)
				if retry < m.Retries {
					select {
					case <-ctx.Done():
						// client canceled request
					case <-time.After(m.BackoffFactor * time.Duration(1<<uint(retry))):
						r = r.WithContext(context.WithValue(ctx, retryContextKey{}, retry+1))
						p.ServeHTTP(w, r)
					}
					return
				}
			}

			m.logf("upstream: %v", err)
			switch err {
			case ErrUnavailable: // load balancer don't have next upstream
				http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			// TODO: timeout is unexposed from http (transport) package
			default:
				http.Error(w, "Bad Gateway", http.StatusBadGateway)
			}
		},
	}

	return p
}

// RoundTrip wraps transport round-trip
func (m *Upstream) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := m.Transport.RoundTrip(r)
	logger.Set(r.Context(), "upstream", r.URL.Host)
	return resp, err
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

type retryContextKey struct{}

func canRetry(r *http.Request) bool {
	if !canMethodRetry(r.Method) {
		return false
	}

	return r.Body == http.NoBody || r.Body == nil
}

func canMethodRetry(method string) bool {
	switch method {
	case
		http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
		http.MethodTrace:
		return true
	default:
		return false
	}
}

// SingleHost creates new single host upstream
func SingleHost(host string, transport http.RoundTripper) *Upstream {
	return New(&singleHostTransport{
		Host:      host,
		Transport: transport,
	})
}

type singleHostTransport struct {
	Host      string
	Transport http.RoundTripper
}

func (l *singleHostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Host = l.Host
	return l.Transport.RoundTrip(r)
}
