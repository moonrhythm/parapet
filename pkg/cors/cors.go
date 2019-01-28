package cors

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// New creates new default cors middleware for public api
func New() *CORS {
	return &CORS{
		AllowAllOrigins: true,
		AllowMethods:    []string{"GET", "POST", "PUT", "PATCH", "DELETE"},
		AllowHeaders:    []string{"Authorization", "Content-Type"},
		MaxAge:          time.Hour,
	}
}

// CORS middleware
type CORS struct {
	AllowAllOrigins  bool
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	ExposeHeaders    []string
	MaxAge           time.Duration
}

// ServeHandler implements middleware interface
func (m CORS) ServeHandler(h http.Handler) http.Handler {
	preflightHeaders := make(http.Header)
	headers := make(http.Header)
	allowOrigins := make(map[string]bool)

	if m.AllowCredentials {
		preflightHeaders.Set("Access-Control-Allow-Credentials", "true")
		headers.Set("Access-Control-Allow-Credentials", "true")
	}
	if len(m.AllowMethods) > 0 {
		preflightHeaders.Set("Access-Control-Allow-Methods", strings.Join(m.AllowMethods, ","))
	}
	if len(m.AllowHeaders) > 0 {
		preflightHeaders.Set("Access-Control-Allow-Headers", strings.Join(m.AllowHeaders, ","))
	}
	if len(m.ExposeHeaders) > 0 {
		headers.Set("Access-Control-Expose-Headers", strings.Join(m.ExposeHeaders, ","))
	}
	if m.MaxAge > time.Duration(0) {
		preflightHeaders.Set("Access-Control-Max-Age", strconv.FormatInt(int64(m.MaxAge/time.Second), 10))
	}
	if m.AllowAllOrigins {
		preflightHeaders.Set("Access-Control-Allow-Origin", "*")
		headers.Set("Access-Control-Allow-Origin", "*")
	} else {
		preflightHeaders.Add("Vary", "Origin")
		preflightHeaders.Add("Vary", "Access-Control-Request-Method")
		preflightHeaders.Add("Vary", "Access-Control-Request-Headers")
		headers.Set("Vary", "Origin")

		for _, v := range m.AllowOrigins {
			allowOrigins[v] = true
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); len(origin) > 0 {
			h := w.Header()
			if !m.AllowAllOrigins {
				if allowOrigins[origin] {
					h.Set("Access-Control-Allow-Origin", origin)
				} else {
					w.WriteHeader(http.StatusForbidden)
					return
				}
			}
			if r.Method == http.MethodOptions {
				for k, v := range preflightHeaders {
					h[k] = v
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			for k, v := range headers {
				h[k] = v
			}
		}
		h.ServeHTTP(w, r)
	})
}
