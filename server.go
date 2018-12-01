package parapet

import (
	"net/http"
	"sync"
)

// Server is the parapet server
type Server struct {
	s    http.Server
	once sync.Once
	ms   []Middleware

	Addr string
}

// New creates new server
func New() *Server {
	return new(Server)
}

// Use pushs middleware
func (s *Server) Use(m Middleware) {
	if s.s.Handler != nil {
		panic("parapet: can not use after serve")
	}
	if m == nil {
		return
	}
	s.ms = append(s.ms, m)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.once.Do(s.configHandler)

	s.s.Handler.ServeHTTP(w, r)
}

func (s *Server) configServer() {
	s.s.Addr = s.Addr
}

func (s *Server) configHandler() {
	h := http.NotFoundHandler()
	for i := len(s.ms); i > 0; i-- {
		h = s.ms[i-1].ServeHandler(h)
	}
	s.s.Handler = h
}

// ListenAndServe starts web server
func (s *Server) ListenAndServe() error {
	s.configServer()

	return s.s.ListenAndServe()
}
