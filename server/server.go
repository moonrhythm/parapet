package server

import (
	"net/http"
	"sync"

	"github.com/moonrhythm/parapet/config"
	"github.com/moonrhythm/parapet/middleware"
)

// Server is the parapet server
type Server struct {
	h      http.Handler
	onceH  sync.Once
	config config.Config
	ms     []middleware.Middleware
}

// New creates new server
func New() *Server {
	return new(Server)
}

// Config loads config
func (s *Server) Config(c config.Config) {
	s.config = c
	s.configServer()
}

// Use pushs middleware
func (s *Server) Use(name string, f middleware.Factory) {
	if f == nil {
		return
	}
	m := f(s.config.Scope(name))
	if m == nil {
		return
	}
	s.ms = append(s.ms, m)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.onceH.Do(func() {
		s.h = http.NotFoundHandler()
		for i := len(s.ms); i > 0; i-- {
			s.h = s.ms[i-1](s.h)
		}
	})

	s.h.ServeHTTP(w, r)
}

func (s *Server) configServer() {
	// TODO
}
