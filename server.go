package parapet

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Server is the parapet server
type Server struct {
	s    http.Server
	once sync.Once
	ms   Middlewares

	Addr              string
	Handler           http.Handler
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	TCPKeepAlive      time.Duration
	GraceTimeout      time.Duration
	ErrorLog          *log.Logger
}

// New creates new server with default config
func New() *Server {
	return &Server{
		ReadTimeout:       time.Minute,
		ReadHeaderTimeout: time.Minute,
		IdleTimeout:       3 * time.Minute,
		TCPKeepAlive:      3 * time.Minute,
		GraceTimeout:      10 * time.Second,
		Handler:           http.NotFoundHandler(),
	}
}

// Use uses middleware
func (s *Server) Use(m Middleware) {
	if s.s.Handler != nil {
		panic("parapet: can not use after serve")
	}
	s.ms.Use(m)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.configHandler()

	s.s.Handler.ServeHTTP(w, r)
}

func (s *Server) configServer() {
	s.configHandler()

	s.s.Addr = s.Addr

	s.s.ReadTimeout = s.ReadTimeout
	s.s.ReadHeaderTimeout = s.ReadHeaderTimeout
	s.s.WriteTimeout = s.WriteTimeout
	s.s.IdleTimeout = s.IdleTimeout
	s.s.ErrorLog = s.ErrorLog
}

func (s *Server) configHandler() {
	s.once.Do(func() {
		h2s := &http2.Server{}
		s.s.Handler = h2c.NewHandler(s.ms.ServeHandler(s.Handler), h2s)
	})
}

// ListenAndServe starts web server
func (s *Server) ListenAndServe() error {
	if s.GraceTimeout <= 0 {
		return s.listenAndServe()
	}

	errChan := make(chan error)

	go func() {
		if err := s.listenAndServe(); err != http.ErrServerClosed {
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
		return s.Shutdown(ctx)
	}
}

func (s *Server) listenAndServe() error {
	addr := s.Addr
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	if s.TCPKeepAlive == 0 {
		return s.Serve(ln)
	}

	return s.Serve(tcpKeepAliveListener{ln.(*net.TCPListener), s.TCPKeepAlive})
}

// Serve serves incoming connections
func (s *Server) Serve(l net.Listener) error {
	s.configServer()

	return s.s.Serve(l)
}

// Shutdown shutdowns server
func (s *Server) Shutdown(ctx context.Context) error {
	return s.s.Shutdown(ctx)
}
