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

type AllowOriginFunc func(origin string) bool

func AllowOrigins(origins ...string) AllowOriginFunc {
	allow := make(map[string]bool)
	for _, v := range origins {
		allow[v] = true
	}
	return func(origin string) bool {
		return allow[origin]
	}
}

// CORS middleware
type CORS struct {
	AllowOrigins     AllowOriginFunc
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	MaxAge           time.Duration
	AllowAllOrigins  bool
	AllowCredentials bool
}

// ServeHandler implements middleware interface
func (m CORS) ServeHandler(h http.Handler) http.Handler {
	if !m.AllowAllOrigins && m.AllowOrigins == nil {
		panic("cors: AllowOrigins must be set if AllowAllOrigins is false")
	}

	preflightHeaders := make(http.Header)
	headers := make(http.Header)

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
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); len(origin) > 0 {
			h := w.Header()
			if !m.AllowAllOrigins {
				if m.AllowOrigins(origin) {
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
