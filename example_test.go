package parapet_test

import (
	"crypto/tls"
	"net/http"
	"strings"
	"time"

	"github.com/moonrhythm/parapet"
)

// Build a server and stack middleware onto it. New() targets a server that runs
// behind a trusted reverse proxy; the last Use is the innermost handler, and the
// chain is applied outermost-first like an onion. Set Handler to the request the
// chain ultimately serves (here, a static handler — usually an upstream proxy).
func ExampleNew() {
	s := parapet.New()
	s.Addr = ":8080"
	s.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	})

	// each Use wraps the previous one; outermost runs first.
	s.Use(parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-App", "parapet")
			h.ServeHTTP(w, r)
		})
	}))

	// s.ListenAndServe() blocks and serves; omitted here.
}

// NewFrontend targets an edge-facing server: it carries read/write timeouts
// suited to the open internet. Attaching a TLSConfig makes it serve HTTPS.
func ExampleNewFrontend() {
	cert, err := parapet.GenerateSelfSignCertificate(parapet.SelfSign{
		CommonName: "example.com",
		Hosts:      []string{"example.com", "10.0.0.1"},
	})
	if err != nil {
		return
	}

	s := parapet.NewFrontend()
	s.Addr = ":443"
	s.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
	// s.Use(redirect.HTTPS()), s.Use(upstream.SingleHost(...)), etc.

	// s.ListenAndServe() blocks and serves; omitted here.
}

// NewBackend targets an internal service that runs behind a parapet frontend or
// another reverse proxy. It enables H2C (cleartext HTTP/2) and trusts forwarded
// headers from the proxy in front of it.
func ExampleNewBackend() {
	s := parapet.NewBackend()
	s.Addr = "10.0.0.5:8080"
	s.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// X-Forwarded-For / X-Real-Ip are populated because the upstream is trusted.
		_, _ = w.Write([]byte(r.Header.Get("X-Real-Ip")))
	})

	// s.ListenAndServe() blocks and serves; omitted here.
}

// Trust forwarded headers (X-Forwarded-For, X-Real-Ip, X-Forwarded-Proto) only
// when the immediate peer is in a known CIDR range, instead of Trusted() which
// trusts every peer. Distrusted peers get these headers overwritten from the
// real connection.
func ExampleTrustCIDRs() {
	s := parapet.New()
	s.TrustProxy = parapet.TrustCIDRs([]string{
		"10.0.0.0/8",     // internal network
		"172.16.0.0/12",  // load balancers
		"192.168.0.0/16", // private range
	})

	// s.ListenAndServe() blocks and serves; omitted here.
}

// Cond branches the chain per request: requests matching If go through Then,
// everything else through Else (or straight to the next handler when Else is
// nil). Here, API paths get one middleware and the rest another.
func ExampleCond() {
	apiOnly := parapet.MiddlewareFunc(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-API", "1")
			h.ServeHTTP(w, r)
		})
	})

	s := parapet.New()
	s.Use(parapet.Cond{
		If: func(r *http.Request) bool {
			return strings.HasPrefix(r.URL.Path, "/api/")
		},
		Then: apiOnly,
	})

	// s.ListenAndServe() blocks and serves; omitted here.
}

// Handler adapts a plain http.HandlerFunc into a Middleware so it can be the
// terminal handler in the chain via Use, without setting Server.Handler.
func ExampleHandler() {
	s := parapet.New()
	s.Use(parapet.Handler(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	// s.ListenAndServe() blocks and serves; omitted here.
}

// GenerateSelfSignCertificate mints an in-memory self-signed certificate, handy
// for local development or internal TLS where a CA-issued cert is unnecessary.
func ExampleGenerateSelfSignCertificate() {
	cert, err := parapet.GenerateSelfSignCertificate(parapet.SelfSign{
		CommonName: "dev.local",
		Hosts:      []string{"dev.local", "localhost", "127.0.0.1"},
		NotAfter:   time.Now().AddDate(1, 0, 0),
	})
	if err != nil {
		return
	}

	s := parapet.NewFrontend()
	s.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
}
