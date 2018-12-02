package parapet

import (
	"time"
)

// NewBackend creates new backend server default config
func NewBackend() *Server {
	return &Server{
		IdleTimeout:  10*time.Minute + 20*time.Second,
		TCPKeepAlive: 10*time.Minute + 20*time.Second,
		GraceTimeout: 10 * time.Second,
	}
}
