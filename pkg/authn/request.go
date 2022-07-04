package authn

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/moonrhythm/parapet/pkg/internal/pool"
)

// Request creates new auth request middleware
func Request(url *url.URL) *RequestAuthenticator {
	return &RequestAuthenticator{
		URL: url,
	}
}

// RequestAuthenticator middleware
type RequestAuthenticator struct {
	URL    *url.URL
	Client *http.Client
}

func (m RequestAuthenticator) ServeHandler(h http.Handler) http.Handler {
	validStatus := map[int]bool{
		http.StatusOK:        true,
		http.StatusNoContent: true,
	}
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

			req, err := http.NewRequestWithContext(r.Context(), r.Method, urlStr, nil)
			if err != nil {
				return err
			}
			req.Header = r.Header.Clone()
			req.Header.Set("Content-Length", "0")
			req.Header.Set("X-Origin-URL", r.URL.String())
			resp, err := client.Do(req)
			if err != nil {
				return &RequestAuthServerError{
					StatusCode:       http.StatusServiceUnavailable,
					IsTransportError: true,
					OriginError:      err,
				}
			}
			if !validStatus[resp.StatusCode] {
				return &RequestAuthServerError{
					StatusCode: resp.StatusCode,
					Response:   resp,
				}
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return nil
		},
		Forbidden: func(w http.ResponseWriter, r *http.Request, err error) {
			var authErr *RequestAuthServerError
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
			io.CopyBuffer(w, resp.Body, buf)
		},
	}.ServeHandler(h)
}

type RequestAuthServerError struct {
	StatusCode       int
	IsTransportError bool
	OriginError      error
	Response         *http.Response
}

func (err *RequestAuthServerError) Error() string {
	return fmt.Sprintf("request auth server error; status=%d; error=%v", err.StatusCode, err.OriginError)
}
