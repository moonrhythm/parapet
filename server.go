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

	"github.com/moonrhythm/parapet/pkg/metric"
)

// Server is the parapet server
type Server struct {
	s          http.Server
	once       sync.Once
	trackState sync.Once
	ms         Middlewares
	onShutdown []func()

	Addr               string
	Handler            http.Handler
	ReadTimeout        time.Duration
	ReadHeaderTimeout  time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	TCPKeepAlivePeriod time.Duration
	GraceTimeout       time.Duration
	WaitBeforeShutdown time.Duration
	ErrorLog           *log.Logger
	TrackConnState     bool
	TrustProxy         bool
	H2C                bool
	ReusePort          bool
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
	s.trackConnState()
}

func (s *Server) trackConnState() {
	if !s.TrackConnState {
		return
	}

	s.trackState.Do(func() {
		type stateChange struct {
			conn  net.Conn
			state http.ConnState
		}

		storage := make(map[net.Conn]http.ConnState)
		chState := make(chan *stateChange, 100)

		s.s.ConnState = func(conn net.Conn, state http.ConnState) {
			if state == http.StateNew {
				return
			}

			chState <- &stateChange{conn, state}
		}

		go func() {
			collect := time.Tick(5 * time.Second)
			for {
				select {
				case s := <-chState:
					switch s.state {
					case http.StateHijacked, http.StateClosed:
						delete(storage, s.conn)
					default:
						storage[s.conn] = s.state
					}
				case <-collect:
					var (
						active int64
						idle   int64
					)
					for _, state := range storage {
						switch state {
						case http.StateActive:
							active++
						case http.StateIdle:
							idle++
						}
					}
					metric.Set("connection:active", active)
					metric.Set("connection:idle", idle)
				}
			}
		}()
	})
}

func (s *Server) configHandler() {
	s.once.Do(func() {
		h := s.ms.ServeHandler(s.Handler)
		if s.TrustProxy {
			h = trustProxy{}.ServeHandler(h)
		} else {
			h = untrustProxy{}.ServeHandler(h)
		}
		h = func(h http.Handler) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				ctx := context.WithValue(r.Context(), ServerContextKey, s)
				h.ServeHTTP(w, r.WithContext(ctx))
			}
		}(h)
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
		return s.Shutdown()
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

// Shutdown gracefully shutdowns server
func (s *Server) Shutdown() error {
	for _, f := range s.onShutdown {
		go f()
	}

	// wait for service to de-registered
	time.Sleep(s.WaitBeforeShutdown)

	ctx, cancel := context.WithTimeout(context.Background(), s.GraceTimeout)
	defer cancel()

	return s.s.Shutdown(ctx)
}

// RegisterOnShutdown calls f when server received SIGTERM
func (s *Server) RegisterOnShutdown(f func()) {
	s.onShutdown = append(s.onShutdown, f)
}
