package hsts

import (
	"net/http"
	"strconv"
	"time"

	"github.com/moonrhythm/parapet/pkg/internal/header"
)

// HSTS middleware
type HSTS struct {
	MaxAge            time.Duration
	IncludeSubDomains bool
	Preload           bool
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header.Set(w.Header(), header.StrictTransportSecurity, hs)
		h.ServeHTTP(w, r)
	})
}
