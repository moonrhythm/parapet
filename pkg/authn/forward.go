package authn

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/moonrhythm/parapet/pkg/internal/header"
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
	for i, h := range m.AuthRequestHeaders {
		m.AuthRequestHeaders[i] = http.CanonicalHeaderKey(h)
	}
	for i, h := range m.AuthResponseHeaders {
		m.AuthResponseHeaders[i] = http.CanonicalHeaderKey(h)
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
				header.Del(req.Header, header.ContentLength)
			} else {
				for _, h := range m.AuthRequestHeaders {
					header.Del(req.Header, h)
					for _, v := range r.Header.Values(h) {
						header.Add(req.Header, h, v)
					}
				}
			}

			header.Set(req.Header, header.XForwardedMethod, r.Method)
			header.Set(req.Header, header.XForwardedHost, r.Host)
			header.Set(req.Header, header.XForwardedURI, r.RequestURI)
			header.Set(req.Header, header.XForwardedProto, header.Get(r.Header, header.XForwardedProto))
			header.Set(req.Header, header.XForwardedFor, header.Get(r.Header, header.XForwardedFor))

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
				header.Del(r.Header, h)
				for _, v := range resp.Header.Values(h) {
					header.Add(r.Header, h, v)
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
	Response         *http.Response
	OriginError      error
	StatusCode       int
	IsTransportError bool
}

func (err *ForwardServerError) Error() string {
	return fmt.Sprintf("request auth server error; status=%d; error=%v", err.StatusCode, err.OriginError)
}
