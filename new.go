package parapet

import (
	"net/http"
	"time"
)

// New creates new middleware server default config
//
// This server should not expose to the internet
// but run behide reverse proxy
func New() *Server {
	return &Server{
		IdleTimeout:  10*time.Minute + 20*time.Second,
		TCPKeepAlive: 10*time.Minute + 20*time.Second,
		GraceTimeout: 30 * time.Second,
		TrustProxy:   true,
		Handler:      http.NotFoundHandler(),
	}
}

// NewFrontend creates new frontend server default config
func NewFrontend() *Server {
	return &Server{
		ReadTimeout:       time.Minute,
		ReadHeaderTimeout: time.Minute,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       75 * time.Second,
		TCPKeepAlive:      75 * time.Second,
		GraceTimeout:      30 * time.Second,
		Handler:           http.NotFoundHandler(),
	}
}

// NewBackend creates new backend server default config
//
// This server use to run behide parapet server
// or run behide other reverse proxy
func NewBackend() *Server {
	return &Server{
		IdleTimeout:  10*time.Minute + 20*time.Second,
		TCPKeepAlive: 10*time.Minute + 20*time.Second,
		GraceTimeout: 30 * time.Second,
		TrustProxy:   true,
		EnableH2C:    true,
		Handler:      http.NotFoundHandler(),
	}
}
