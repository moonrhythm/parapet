package cors

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/moonrhythm/parapet/pkg/internal/header"
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
		header.Set(preflightHeaders, header.AccessControlAllowCredentials, "true")
		header.Set(headers, header.AccessControlAllowCredentials, "true")
	}
	if len(m.AllowMethods) > 0 {
		header.Set(preflightHeaders, header.AccessControlAllowMethods, strings.Join(m.AllowMethods, ","))
	}
	if len(m.AllowHeaders) > 0 {
		header.Set(preflightHeaders, header.AccessControlAllowHeaders, strings.Join(m.AllowHeaders, ","))
	}
	if len(m.ExposeHeaders) > 0 {
		header.Set(headers, header.AccessControlExposeHeaders, strings.Join(m.ExposeHeaders, ","))
	}
	if m.MaxAge > time.Duration(0) {
		header.Set(preflightHeaders, header.AccessControlMaxAge, strconv.FormatInt(int64(m.MaxAge/time.Second), 10))
	}
	if m.AllowAllOrigins {
		header.Set(preflightHeaders, header.AccessControlAllowOrigin, "*")
		header.Set(headers, header.AccessControlAllowOrigin, "*")
	} else {
		header.Add(preflightHeaders, header.Vary, header.Origin)
		header.Add(preflightHeaders, header.Vary, header.AccessControlRequestMethod)
		header.Add(preflightHeaders, header.Vary, header.AccessControlRequestHeaders)
		header.Set(headers, header.Vary, header.Origin)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := header.Get(r.Header, header.Origin); len(origin) > 0 {
			h := w.Header()
			if !m.AllowAllOrigins {
				if m.AllowOrigins(origin) {
					header.Set(h, header.AccessControlAllowOrigin, origin)
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
