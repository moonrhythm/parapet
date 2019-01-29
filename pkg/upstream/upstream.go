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
		},
		BufferPool: bytesPool,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			logger.Set(r.Context(), "upstream", r.URL.Host)
			return m.Transport.RoundTrip(r)
		}),
		ErrorLog: m.ErrorLog,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if err == context.Canceled {
				// client canceled request
				return
			}

			ctx := r.Context()
			retry, _ := ctx.Value(retryContextKey{}).(int)
			if retry < m.Retries {
				time.Sleep(m.BackoffFactor * time.Duration(1<<uint(retry)))
				if err == context.Canceled {
					// client canceled request
					return
				}

				r = r.WithContext(context.WithValue(ctx, retryContextKey{}, retry+1))
				p.ServeHTTP(w, r)
				return
			}

			m.logf("upstream: %v", err)
			switch err {
			case ErrUnavailable:
				http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			// TODO: timeout is unexposed from http (transport) package
			default:
				http.Error(w, "Bad Gateway", http.StatusBadGateway)
			}
		},
	}

	return p
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

type roundTripperFunc func(r *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type retryContextKey struct{}
