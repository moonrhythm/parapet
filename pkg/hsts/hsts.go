package hsts

import (
	"net/http"
	"strconv"
	"time"

	"github.com/moonrhythm/parapet/pkg/header"
)

// HSTS middleware
type HSTS struct {
	MaxAge            time.Duration
	IncludeSubDomains bool
	Preload           bool

	// ShareValueSlice writes the Strict-Transport-Security value from a single
	// slice shared across requests instead of allocating one per request. The
	// value is fixed at construction, so this is safe as long as nothing
	// mutates the response header value slice in place. Off by default; see
	// header.SetShared.
	ShareValueSlice bool
}

// Default returns default hsts
func Default() *HSTS {
	return &HSTS{
		MaxAge:            31536000 * time.Second,
		IncludeSubDomains: false,
		Preload:           false,
	}
}

// Preload returns hsts preload
func Preload() *HSTS {
	return &HSTS{
		MaxAge:            63072000 * time.Second,
		IncludeSubDomains: true,
		Preload:           true,
	}
}

// ServeHandler implements middleware interface
func (m HSTS) ServeHandler(h http.Handler) http.Handler {
	hs := "max-age=" + strconv.FormatInt(int64(m.MaxAge/time.Second), 10)
	if m.IncludeSubDomains {
		hs += "; includeSubDomains"
	}
	if m.Preload {
		hs += "; preload"
	}

	if m.ShareValueSlice {
		// Value is fixed for the life of this middleware: build the slice once
		// and share it across requests instead of allocating per request.
		hsValue := []string{hs}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header.SetShared(w.Header(), header.StrictTransportSecurity, hsValue)
			h.ServeHTTP(w, r)
		})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header.Set(w.Header(), header.StrictTransportSecurity, hs)
		h.ServeHTTP(w, r)
	})
}
