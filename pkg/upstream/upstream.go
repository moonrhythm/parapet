package upstream

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
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
	Transport   http.RoundTripper
	ErrorLog    *log.Logger
	OnRoundTrip RoundTripFunc // observe each origin round-trip (nil disables); see prom.Upstream

	// RetryPolicy decides whether a request is eligible to be retried after a
	// transport error. nil uses the default canRetry: an idempotent method
	// (GET/HEAD/OPTIONS/TRACE) AND a body that is either absent or rewindable
	// (r.Body is nil/http.NoBody, OR r.GetBody != nil). Set it to widen
	// eligibility — e.g. to retry an idempotent PUT/DELETE — or to narrow it.
	//
	// RETRY AMPLIFICATION — READ BEFORE WIDENING. An eligible request may be sent
	// to upstreams up to Retries+1 times (one initial attempt plus Retries
	// re-attempts). If the same Upstream is also fronted by a HedgingLoadBalancer,
	// each of those attempts can additionally fan out to MaxHedge speculative
	// copies, so the worst-case origin load multiplies (≈ (Retries+1) × (MaxHedge+1)).
	// Size Retries (and MaxHedge) for that ceiling, and only mark a request
	// retryable here when the upstream is genuinely idempotent for it — a retried
	// non-idempotent request can double-apply a side effect (a duplicate POST, a
	// second charge). A body-bearing request is only retried when r.GetBody is set
	// (so each attempt can be rewound to the full body); without GetBody, even an
	// eligible method is not retried.
	RetryPolicy func(r *http.Request) bool

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
			// Resolve dot-segments on the request path before joining so that
			// requests like "/foo/../bar" cannot escape the configured prefix.
			req.URL.Path = singleJoiningSlash(targetPath.Path, cleanRequestPath(req.URL.Path))
			// RawPath may no longer correspond to Path after cleaning; clear it
			// so net/url re-encodes Path on write.
			req.URL.RawPath = ""

			if targetPath.RawQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = targetPath.RawQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = targetPath.RawQuery + "&" + req.URL.RawQuery
			}

			if m.Host != "" {
				req.Host = m.Host
			}
		},
		BufferPool: bytesPool,
		Transport:  &m,
		ErrorLog:   m.ErrorLog,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if err == context.Canceled {
				// client canceled request
				return
			}

			if m.retryable(r) {
				ctx := r.Context()
				retry, _ := ctx.Value(retryContextKey{}).(int)
				if retry < m.Retries {
					select {
					case <-ctx.Done():
						// client canceled request
						return
					case <-time.After(m.BackoffFactor * time.Duration(1<<uint(retry))):
						// Rewind a body-bearing request before re-attempting: the
						// previous attempt consumed r.Body, so a fresh copy from
						// GetBody is required or the retry would send an empty body.
						// If the rewind fails, give up retrying and fall through to
						// surface the original transport error below.
						if r.GetBody != nil {
							body, gerr := r.GetBody()
							if gerr != nil {
								m.logf("upstream: retry rewind: %v", gerr)
								break
							}
							r.Body = body
						}
						r = r.WithContext(context.WithValue(ctx, retryContextKey{}, retry+1))
						p.ServeHTTP(w, r)
						return
					}
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "" // disable httputil.ReverseProxy to add X-Forwarded-For since we already added
		p.ServeHTTP(w, r)
	})
}

// RoundTrip wraps transport round-trip
func (m *Upstream) RoundTrip(r *http.Request) (*http.Response, error) {
	// The transport (load balancer / single-host) sets the resolved target. Clear it
	// first so a request shed before any pick (a reliability balancer returning
	// ErrUnavailable) reports an empty host, not a stale target left from a prior
	// retry attempt — making the fast-reject metric's host label unambiguous.
	r.URL.Host = ""
	start := time.Now()
	resp, err := m.Transport.RoundTrip(r)
	logger.Set(r.Context(), "upstream", r.URL.Host)
	if m.OnRoundTrip != nil {
		// r.URL.Host is the target just resolved; Duration is the time to response
		// headers, before the body streams; Attempt is the retry index (0 first try).
		attempt, _ := r.Context().Value(retryContextKey{}).(int)
		info := RoundTripInfo{Host: r.URL.Host, Duration: time.Since(start), Err: err, Attempt: attempt}
		if resp != nil {
			info.Status = resp.StatusCode
		}
		m.OnRoundTrip(r, info)
	}
	return resp, err
}

// cleanRequestPath resolves dot-segments from the request path while
// preserving a trailing slash. An empty result is normalized to "/".
func cleanRequestPath(p string) string {
	if p == "" {
		return "/"
	}
	trailing := strings.HasSuffix(p, "/")
	cleaned := path.Clean(p)
	if cleaned == "." {
		cleaned = "/"
	}
	if trailing && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
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

// retryable reports whether r may be retried, consulting the operator's
// RetryPolicy when set and falling back to the default canRetry otherwise.
func (m *Upstream) retryable(r *http.Request) bool {
	if m.RetryPolicy != nil {
		return m.RetryPolicy(r)
	}
	return canRetry(r)
}

// canRetry is the default retry-eligibility rule: an idempotent method whose
// body is either absent or rewindable. A body-bearing request qualifies only
// when r.GetBody is set (the stdlib rewind hook), so each re-attempt can be
// reset to the full body before being sent again — otherwise a retry would
// transmit a consumed/empty body. The method set (GET/HEAD/OPTIONS/TRACE) is
// unchanged; retrying an idempotent PUT/DELETE is opt-in via a custom
// Upstream.RetryPolicy.
func canRetry(r *http.Request) bool {
	if !canMethodRetry(r.Method) {
		return false
	}
	if r.Body == http.NoBody || r.Body == nil {
		return true
	}
	// A body-bearing request is retryable only if it can be rewound.
	return r.GetBody != nil
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
	Transport http.RoundTripper
	Host      string
}

func (l *singleHostTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Host = l.Host
	return l.Transport.RoundTrip(r)
}
