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

	"github.com/kavu/go_reuseport"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// Server is the parapet server
type Server struct {
	s    http.Server
	once sync.Once
	ms   Middlewares

	Addr               string
	Handler            http.Handler
	ReadTimeout        time.Duration
	ReadHeaderTimeout  time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	TCPKeepAlivePeriod time.Duration
	GraceTimeout       time.Duration
	ErrorLog           *log.Logger
	TrustProxy         bool
	H2C                bool
	ReusePort          bool
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
		h := s.ms.ServeHandler(s.Handler)
		h = trustProxy{s.TrustProxy}.ServeHandler(h)
		if s.H2C {
			h2s := &http2.Server{}
			h = h2c.NewHandler(h, h2s)
		}
		s.s.Handler = h
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

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGTERM)

	select {
	case err := <-errChan:
		return err
	case <-shutdown:
		// wait for service to deregistered
		time.Sleep(5 * time.Second)

		ctx, cancel := context.WithTimeout(context.Background(), s.GraceTimeout)
		defer cancel()
		return s.Shutdown(ctx)
	}
}

func (s *Server) listenAndServe() error {
	addr := s.Addr
	if addr == "" {
		addr = ":http"
	}

	var ln net.Listener
	var err error
	if s.ReusePort {
		ln, err = reuseport.NewReusablePortListener("tcp", addr)
	} else {
		ln, err = net.Listen("tcp", addr)
	}
	if err != nil {
		return err
	}

	if s.TCPKeepAlivePeriod == 0 {
		return s.Serve(ln)
	}

	return s.Serve(tcpListener{ln.(*net.TCPListener), s.TCPKeepAlivePeriod})
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
