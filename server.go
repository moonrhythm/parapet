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
)

// Server is the parapet server
//
//nolint:govet
type Server struct {
	s    http.Server
	once sync.Once
	ms   Middlewares

	// muShutdown guards onShutdown and shuttingDown. Both are cold-path:
	// registration (RegisterOnShutdown, possibly from request goroutines via
	// lazy once.Do in pkg/healthz and pkg/upstream) and the one-shot shutdown
	// snapshot. It is never taken on the per-request hot path.
	muShutdown   sync.Mutex
	onShutdown   []func()
	shuttingDown bool

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

	// ShareProtoSlice makes the proxy write a single shared []string for the
	// X-Forwarded-Proto header ("http"/"https") instead of allocating a fresh
	// slice per request, saving one allocation on every request that sets it.
	//
	// UNSAFE if any middleware mutates the X-Forwarded-Proto value slice in
	// place — e.g. headers.MapRequest("X-Forwarded-Proto", …) or code doing
	// r.Header["X-Forwarded-Proto"][0] = …; the mutation would corrupt the
	// shared slice for every subsequent request. Appending (headers.AddRequest)
	// is safe. Enable it only if you control the whole middleware chain. Like
	// the other fields it must be set before serving; setting it afterwards has
	// no effect.
	ShareProtoSlice bool
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
			Trust:           s.TrustProxy,
			Handler:         h,
			shareProtoSlice: s.ShareProtoSlice,
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
			p := new(http.Protocols)
			p.SetHTTP1(true)
			p.SetUnencryptedHTTP2(true)
			s.s.Protocols = p
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
	// Flip the shutting-down flag and take the callback snapshot in one
	// critical section, then release before firing/sleeping/draining so the
	// lock is never held across time.Sleep, go f(), or s.s.Shutdown. After
	// this point RegisterOnShutdown sees shuttingDown==true and runs f itself,
	// so no registration can be lost between the snapshot and the flag flip.
	s.muShutdown.Lock()
	first := !s.shuttingDown
	s.shuttingDown = true
	fns := s.onShutdown
	s.onShutdown = nil
	s.muShutdown.Unlock()

	if first {
		for _, f := range fns {
			go f()
		}

		// wait for service to de-registered
		time.Sleep(s.WaitBeforeShutdown)
	}

	ctx := context.Background()
	if s.GraceTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.GraceTimeout)
		defer cancel()
	}

	return s.s.Shutdown(ctx)
}

// RegisterOnShutdown registers f to run when the server begins graceful
// shutdown (Shutdown, e.g. on SIGTERM). It is safe to call concurrently,
// including from request handlers while the server is already shutting down.
//
// Ordering relative to Shutdown:
//   - Registered before Shutdown begins: f is launched by Shutdown via go f().
//   - Registered after Shutdown has begun (concurrently or later): f is
//     launched immediately by this call via go f(), instead of being queued —
//     so a registration that loses the race to Shutdown is never dropped.
//
// In both cases f runs on its own goroutine, exactly once: the first Shutdown
// fires its snapshot and clears it, so a later Shutdown re-fires nothing, and a
// registration that loses the race to Shutdown takes the run-now path rather
// than being dropped. RegisterOnShutdown does not wait for f to complete. f
// must still be safe to run at any time, because the run-now launch can happen
// on an arbitrary request goroutine concurrently with the shutdown sequence.
// In-tree callers satisfy this: pkg/healthz does an atomic flag store, and
// pkg/upstream ActiveHealthCheck.Close is idempotent.
func (s *Server) RegisterOnShutdown(f func()) {
	s.muShutdown.Lock()
	if s.shuttingDown {
		s.muShutdown.Unlock()
		go f()
		return
	}
	s.onShutdown = append(s.onShutdown, f)
	s.muShutdown.Unlock()
}

// ModifyConnection registers f to wrap every accepted connection before it is
// handed to the HTTP server (e.g. PROXY-protocol unwrapping, byte accounting).
//
// It must be called before serving begins, from the same goroutine that sets
// the server up: listenAndServe reads the registered set once and hands it to
// the listener, which then owns it for its lifetime. Unlike RegisterOnShutdown
// it is NOT safe to call concurrently with, or after, serving — all in-tree
// callers (pkg/prom.Networks, pkg/proxyprotocol) follow this. A late call would
// not affect already-accepted listeners and could race the accept loop.
func (s *Server) ModifyConnection(f func(conn net.Conn) net.Conn) {
	s.modifyConn = append(s.modifyConn, f)
}

func (s *Server) isTLS() bool {
	return s.TLSConfig != nil
}
