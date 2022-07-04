package authn

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
			defer resp.Body.Close()
			defer io.Copy(io.Discard, resp.Body)

			if !validStatus[resp.StatusCode] {
				return &RequestAuthServerError{StatusCode: resp.StatusCode}
			}
			return nil
		},
		Forbidden: func(w http.ResponseWriter, r *http.Request, err error) {
			var authErr *RequestAuthServerError
			if errors.As(err, &authErr) {
				if authErr.IsTransportError {
					http.Error(w, "Auth Server Unavailable", authErr.StatusCode)
					return
				}
				http.Error(w, "", authErr.StatusCode)
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		},
	}.ServeHandler(h)
}

type RequestAuthServerError struct {
	StatusCode       int
	IsTransportError bool
	OriginError      error
}

func (err *RequestAuthServerError) Error() string {
	return fmt.Sprintf("request auth server error; status=%d; error=%v", err.StatusCode, err.OriginError)
}
