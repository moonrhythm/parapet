package parapet

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Server is the parapet server
type Server struct {
	s    http.Server
	once sync.Once
	ms   Middlewares

	Addr         string
	GraceTimeout time.Duration
}

// New creates new server
func New() *Server {
	return new(Server)
}

// Use uses middleware
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
	s.configHandler()

	s.s.Handler.ServeHTTP(w, r)
}

func (s *Server) configServer() {
	s.configHandler()

	s.s.Addr = s.Addr

	if s.GraceTimeout == 0 {
		s.GraceTimeout = 10 * time.Second
	}
}

func (s *Server) configHandler() {
	s.once.Do(func() {
		s.s.Handler = s.ms.ServeHandler(http.NotFoundHandler())
	})
}

// ListenAndServe starts web server
func (s *Server) ListenAndServe() error {
	s.configServer()

	if s.GraceTimeout <= 0 {
		return s.s.ListenAndServe()
	}

	errChan := make(chan error)

	go func() {
		if err := s.s.ListenAndServe(); err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	stop := make(chan os.Signal, 2)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)

	ctx, cancel := context.WithTimeout(context.Background(), s.GraceTimeout)
	defer cancel()

	select {
	case err := <-errChan:
		return err
	case <-stop:
		return s.s.Shutdown(ctx)
	}
}

// Shutdown shutdowns server
func (s *Server) Shutdown(ctx context.Context) error {
	return s.s.Shutdown(ctx)
}
