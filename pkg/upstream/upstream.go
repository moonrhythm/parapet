package upstream

import (
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

// ServeHandler implements middleware interface
func (m *Upstream) ServeHandler(h http.Handler) http.Handler {
	target, err := url.Parse(m.Target)
	if err != nil {
		panic(err)
	}

	if m.DialTimeout == 0 {
		m.DialTimeout = 5 * time.Second
	}
	if m.TCPKeepAlive == 0 {
		m.TCPKeepAlive = 10 * time.Minute
	}
	if m.MaxIdleConns == 0 {
		m.MaxIdleConns = 100
	}
	if m.IdleConnTimeout == 0 {
		m.IdleConnTimeout = 10 * time.Minute
	}

	targetQuery := target.RawQuery
	r := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = singleJoiningSlash(target.Path, req.URL.Path)
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
		Transport:  m.newTransport(),
		ErrorLog:   m.ErrorLog,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
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
