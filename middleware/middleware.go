package middleware

import (
	"net/http"

	"github.com/moonrhythm/parapet/config"
)

// Middleware is the http middleware
type Middleware func(http.Handler) http.Handler

// Factory is the middleware factory
type Factory func(config.Config) Middleware
