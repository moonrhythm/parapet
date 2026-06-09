package hsts_test

import (
	"time"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/hsts"
)

// Add a Strict-Transport-Security response header with sensible defaults
// (max-age of one year, no includeSubDomains, no preload).
func ExampleDefault() {
	s := parapet.New()
	s.Use(hsts.Default())
}

// Use the preload-ready policy: a two-year max-age with includeSubDomains and
// preload, suitable for submission to the HSTS preload list.
func ExamplePreload() {
	s := parapet.New()
	s.Use(hsts.Preload())
}

// Configure the policy explicitly, e.g. a shorter max-age while rolling HSTS out
// across an apex domain and its subdomains.
func ExampleHSTS() {
	s := parapet.New()
	s.Use(&hsts.HSTS{
		MaxAge:            90 * 24 * time.Hour,
		IncludeSubDomains: true,
	})
}

// Share the (fixed) header value slice across requests to avoid a per-request
// allocation on the hot path. Safe because the value never changes after
// construction.
func ExampleHSTS_shareValueSlice() {
	s := parapet.New()
	s.Use(&hsts.HSTS{
		MaxAge:          365 * 24 * time.Hour,
		ShareValueSlice: true,
	})
}
