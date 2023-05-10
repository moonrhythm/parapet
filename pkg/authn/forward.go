package authn

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/moonrhythm/parapet/pkg/internal/pool"
)

// Forward creates new auth request middleware
func Forward(url *url.URL) *ForwardAuthenticator {
	return &ForwardAuthenticator{
		URL: url,
	}
}

// ForwardAuthenticator middleware
type ForwardAuthenticator struct {
	URL                 *url.URL
	Client              *http.Client
	AuthRequestHeaders  []string
	AuthResponseHeaders []string
}

func (m ForwardAuthenticator) validStatusCode(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}

func (m ForwardAuthenticator) ServeHandler(h http.Handler) http.Handler {
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	var urlStr string
	if m.URL != nil {
		urlStr = m.URL.String()
	}
	return Authenticator{
		Authenticate: func(r *http.Request) error {
			if urlStr == "" {
				return errors.New("missing url")
			}

			req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, urlStr, nil)
			if err != nil {
				return err
			}
			if len(m.AuthRequestHeaders) == 0 {
				req.Header = r.Header.Clone()
				req.Header.Del("Content-Length")
			} else {
				for _, h := range m.AuthRequestHeaders {
					req.Header.Del(h)
					for _, v := range r.Header.Values(h) {
						req.Header.Add(h, v)
					}
				}
			}

			req.Header.Set("X-Forwarded-Method", r.Method)
			req.Header.Set("X-Forwarded-Host", r.Host)
			req.Header.Set("X-Forwarded-Uri", r.RequestURI)
			req.Header.Set("X-Forwarded-Proto", r.Header.Get("X-Forwarded-Proto"))
			req.Header.Set("X-Forwarded-For", r.Header.Get("X-Forwarded-For"))

			resp, err := client.Do(req)
			if err != nil {
				return &ForwardServerError{
					StatusCode:       http.StatusServiceUnavailable,
					IsTransportError: true,
					OriginError:      err,
				}
			}

			if !m.validStatusCode(resp.StatusCode) {
				return &ForwardServerError{
					StatusCode: resp.StatusCode,
					Response:   resp,
				}
			}

			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			for _, h := range m.AuthResponseHeaders {
				r.Header.Del(h)
				for _, v := range resp.Header.Values(h) {
					r.Header.Add(h, v)
				}
			}

			return nil
		},
		Forbidden: func(w http.ResponseWriter, r *http.Request, err error) {
			var authErr *ForwardServerError
			if !errors.As(err, &authErr) {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			// can not connect to auth server
			if authErr.IsTransportError {
				http.Error(w, "Auth Server Unavailable", authErr.StatusCode)
				return
			}

			// auth server not allow request
			resp := authErr.Response
			defer resp.Body.Close()
			defer io.Copy(io.Discard, resp.Body)

			wh := w.Header()
			for k, v := range resp.Header {
				wh[k] = v
			}
			w.WriteHeader(resp.StatusCode)

			buf := pool.Get()
			defer pool.Put(buf)
			io.CopyBuffer(w, resp.Body, *buf)
		},
	}.ServeHandler(h)
}

type ForwardServerError struct {
	StatusCode       int
	IsTransportError bool
	OriginError      error
	Response         *http.Response
}

func (err *ForwardServerError) Error() string {
	return fmt.Sprintf("request auth server error; status=%d; error=%v", err.StatusCode, err.OriginError)
}
