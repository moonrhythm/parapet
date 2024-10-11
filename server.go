package parapet

import (
	"context"
	"crypto/tls"
	"errors"
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
//
//nolint:govet
type Server struct {
	s          http.Server
	once       sync.Once
	ms         Middlewares
	onShutdown []func()
	modifyConn []func(conn net.Conn) net.Conn

	Addr               string
	Handler            http.Handler
	ReadTimeout        time.Duration
	ReadHeaderTimeout  time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	MaxHeaderBytes     int
	TCPKeepAlivePeriod time.Duration
	GraceTimeout       time.Duration
	WaitBeforeShutdown time.Duration
	ErrorLog           *log.Logger
	TrustProxy         Conditional
	H2C                bool
	ReusePort          bool
	ConnState          func(conn net.Conn, state http.ConnState)
	TLSConfig          *tls.Config
	BaseContext        func(net.Listener) context.Context
}

type serverContextKey struct{}

// ServerContextKey is the context key that store *parapet.Server
var ServerContextKey = serverContextKey{}

// Use uses middleware
func (s *Server) Use(m Middleware) {
	if s.s.Handler != nil {
		panic("parapet: can not use after serve")
	}
	s.ms.Use(m)
}

func (s *Server) UseFunc(m MiddlewareFunc) {
	s.Use(m)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.configHandler()

	s.s.Handler.ServeHTTP(w, r)
}

func (s *Server) configServer() {
	s.configHandler()

	s.s.Addr = s.Addr
	s.s.ConnState = s.ConnState

	s.s.ReadTimeout = s.ReadTimeout
	s.s.ReadHeaderTimeout = s.ReadHeaderTimeout
	s.s.WriteTimeout = s.WriteTimeout
	s.s.IdleTimeout = s.IdleTimeout
	s.s.MaxHeaderBytes = s.MaxHeaderBytes
	s.s.ErrorLog = s.ErrorLog
	s.s.TLSConfig = s.TLSConfig
}

func (s *Server) configHandler() {
	s.once.Do(func() {
		h := s.ms.ServeHandler(s.Handler)
		h = &proxy{
			Trust:   s.TrustProxy,
			Handler: h,
		}
		s.s.BaseContext = func(l net.Listener) context.Context {
			ctx := context.Background()
			if s.BaseContext != nil {
				ctx = s.BaseContext(l)
			}
			ctx = context.WithValue(ctx, ServerContextKey, s)
			return ctx
		}
		if s.H2C {
			h = h2c.NewHandler(h, &http2.Server{})
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
		if err := s.listenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGTERM)

	select {
	case err := <-errChan:
		return err
	case <-shutdown:
		return s.Shutdown()
	}
}

func (s *Server) listenAndServe() error {
	addr := s.Addr
	if addr == "" {
		if s.isTLS() {
			addr = ":443"
		} else {
			addr = ":http"
		}
	}

	var ln net.Listener
	var err error
	if s.ReusePort {
		ln, err = reuseport.NewReusablePortListener("tcp", addr)
		if err != nil {
			return err
		}
		ln = &tcpListener{
			TCPListener:     ln.(*net.TCPListener),
			KeepAlivePeriod: s.TCPKeepAlivePeriod,
		}
	} else {
		lc := net.ListenConfig{
			KeepAlive: s.TCPKeepAlivePeriod,
		}
		ln, err = lc.Listen(context.Background(), "tcp", addr)
		if err != nil {
			return err
		}
	}

	if len(s.modifyConn) > 0 {
		ln = &modifyConnListener{
			Listener:   ln,
			ModifyConn: s.modifyConn,
		}
	}

	return s.Serve(ln)
}

// Serve serves incoming connections
func (s *Server) Serve(l net.Listener) error {
	s.configServer()

	if s.isTLS() {
		return s.s.ServeTLS(l, "", "")
	}

	return s.s.Serve(l)
}

// Shutdown gracefully shutdowns server
func (s *Server) Shutdown() error {
	for _, f := range s.onShutdown {
		go f()
	}

	// wait for service to de-registered
	time.Sleep(s.WaitBeforeShutdown)

	ctx := context.Background()
	if s.GraceTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.GraceTimeout)
		defer cancel()
	}

	return s.s.Shutdown(ctx)
}

// RegisterOnShutdown calls f when server received SIGTERM
func (s *Server) RegisterOnShutdown(f func()) {
	s.onShutdown = append(s.onShutdown, f)
}

// ModifyConnection modifies connection before send to http
func (s *Server) ModifyConnection(f func(conn net.Conn) net.Conn) {
	s.modifyConn = append(s.modifyConn, f)
}

func (s *Server) isTLS() bool {
	return s.TLSConfig != nil
}
