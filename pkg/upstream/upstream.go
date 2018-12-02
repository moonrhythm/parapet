package upstream

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// Upstream middleware
type Upstream struct {
	Target                string
	Host                  string // override host
	DialTimeout           time.Duration
	TCPKeepAlive          time.Duration
	DisableKeepAlives     bool
	MaxIdleConns          int
	IdleConnTimeout       time.Duration
	ResponseHeaderTimeout time.Duration
	VerifyCA              bool
	ErrorLog              *log.Logger
}

func (m *Upstream) logf(format string, v ...interface{}) {
	if m.ErrorLog == nil {
		log.Printf(format, v...)
		return
	}
	m.ErrorLog.Printf(format, v...)
}

// ServeHandler implements middleware interface
func (m *Upstream) ServeHandler(h http.Handler) http.Handler {
	target, err := url.Parse(m.Target)
	if err != nil {
		panic(err)
	}

	targetQuery := target.RawQuery
	r := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme

			switch target.Scheme {
			case "unix":
				req.URL.Host = "/" + target.Host + "/" + target.Path
			default:
				req.URL.Host = target.Host
				req.URL.Path = singleJoiningSlash(target.Path, req.URL.Path)
			}

			if targetQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = targetQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
			}
			if _, ok := req.Header["User-Agent"]; !ok {
				req.Header.Set("User-Agent", "")
			}

			if m.Host != "" {
				req.Host = m.Host
			}
		},
		BufferPool: bytesPool,
		Transport:  m.transport(target.Scheme),
		ErrorLog:   m.ErrorLog,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if err == context.Canceled {
				// client canceled request
				return
			}

			m.logf("upstream: %v", err)
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		},
	}

	return r
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
