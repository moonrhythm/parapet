package parapet

import (
	"net/http"
	"time"
)

// New creates new middleware server default config
func New() *Server {
	return &Server{
		IdleTimeout:  10*time.Minute + 20*time.Second,
		TCPKeepAlive: 10*time.Minute + 20*time.Second,
		GraceTimeout: 30 * time.Second,
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
func NewBackend() *Server {
	return &Server{
		IdleTimeout:  10*time.Minute + 20*time.Second,
		TCPKeepAlive: 10*time.Minute + 20*time.Second,
		GraceTimeout: 30 * time.Second,
		Handler:      http.NotFoundHandler(),
	}
}
