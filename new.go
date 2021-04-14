package parapet

import (
	"net/http"
	"time"
)

// New creates new middleware server default config
//
// This server should not expose to the internet
// but run behind reverse proxy
func New() *Server {
	return &Server{
		IdleTimeout:        620 * time.Second,
		TCPKeepAlivePeriod: 3 * time.Minute,
		GraceTimeout:       30 * time.Second,
		WaitBeforeShutdown: 10 * time.Second,
		TrustProxy:         Trusted(),
		Handler:            http.NotFoundHandler(),
	}
}

// NewFrontend creates new frontend server default config
func NewFrontend() *Server {
	return &Server{
		ReadHeaderTimeout:  10 * time.Second,
		ReadTimeout:        1 * time.Minute,
		WriteTimeout:       1 * time.Minute,
		IdleTimeout:        75 * time.Second,
		TCPKeepAlivePeriod: 60 * time.Second,
		GraceTimeout:       30 * time.Second,
		WaitBeforeShutdown: 10 * time.Second,
		Handler:            http.NotFoundHandler(),
	}
}

// NewBackend creates new backend server default config
//
// This server use to run behind parapet server
// or run behind other reverse proxy
func NewBackend() *Server {
	return &Server{
		IdleTimeout:        620 * time.Second,
		TCPKeepAlivePeriod: 3 * time.Minute,
		GraceTimeout:       30 * time.Second,
		WaitBeforeShutdown: 10 * time.Second,
		TrustProxy:         Trusted(),
		H2C:                true,
		Handler:            http.NotFoundHandler(),
	}
}
