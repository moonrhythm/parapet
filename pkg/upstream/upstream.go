package upstream

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/moonrhythm/parapet/pkg/logger"
)

// Upstream controls request flow to upstream server via load balancer
type Upstream struct {
	Transport http.RoundTripper
	ErrorLog  *log.Logger
	Host      string // override host
	Path      string // target prefix path
}

// New creates new upstream
func New(transport http.RoundTripper) *Upstream {
	return &Upstream{
		Transport: transport,
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
	targetPath, err := url.ParseRequestURI(m.Path)
	if err != nil {
		panic(err)
	}

	p := &httputil.ReverseProxy{
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

			m.logf("upstream: %v", err)
			// TODO: retry ?
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
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
