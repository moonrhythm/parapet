// Package mirror provides a request-shadowing (traffic-mirroring) middleware: it
// tees a copy of matched/sampled REQUESTS to a separate destination chain (a
// "canary"), fire-and-forget, without ever affecting the primary request or its
// response. Use it to exercise a new build with real production traffic.
package mirror

import (
	"bytes"
	"context"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moonrhythm/parapet"
)

// hopByHop headers are connection-specific and must not be forwarded to the mirror
// (the same set as cache/tee.go).
var hopByHop = map[string]struct{}{
	"Connection":          {},
	"Proxy-Connection":    {},
	"Keep-Alive":          {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
}

// New creates a request-mirroring middleware. Configure the mirror destination with
// Use (typically upstream.SingleHost, or a balancer wrapped by upstream.New) and any
// config fields, then install it ahead of the real chain.
func New() *Mirror { return &Mirror{} }

// Mirror is a traffic-shadowing Middleware. It tees the REQUEST (not the response) of
// matched/sampled requests to its destination chain on a fixed worker pool,
// fire-and-forget, and never affects the primary request. The primary always runs
// unmodified; a mirror that is slow, full, or panicking is dropped or recovered, not
// propagated.
//
// Config fields and the destination chain (Use) are read once on the first
// ServeHandler and then FROZEN — set them before serving; a later Use is silently
// ignored (unlike block.Block, which rebuilds per request). Effective mirror
// concurrency is Workers, not Workers+QueueSize: the queue only absorbs bursts that
// drain at worker speed, so under a slow-but-responsive canary the pool caps at
// Workers and the surplus is dropped (DroppedFull) — size Workers for the canary's
// latency, and wire Observe to watch the drop counters.
//
// The worker pool is started on first use and lives for the PROCESS LIFETIME (no
// Close): construct one Mirror per destination and reuse it. On graceful shutdown
// in-flight/queued mirrors are abandoned (each bounded only by Timeout), which is
// acceptable for fire-and-forget shadow traffic. End-to-end credentials
// (Authorization, Cookie) are replayed to the canary by design (replay fidelity);
// use Match to exclude sensitive routes from shadowing.
//
//nolint:govet // fields grouped by role (state, then config) for readability
type Mirror struct {
	once    sync.Once
	jobs    chan *mirrorJob
	handler http.Handler        // built from ms in init()
	ms      parapet.Middlewares // the mirror destination chain

	dispatched   atomic.Uint64
	dropFull     atomic.Uint64
	dropOversize atomic.Uint64
	completed    atomic.Uint64
	panicked     atomic.Uint64

	// Match selects which requests are eligible to mirror; nil matches all.
	Match func(r *http.Request) bool

	// Observe receives one MirrorInfo per decision/result; nil disables it. It is
	// called SYNCHRONOUSLY and concurrently — on the request goroutine for the
	// dispatch/drop outcomes (so it must be fast and non-blocking, or it stalls the
	// primary) and on worker goroutines for completed/panicked — so it must be safe
	// for concurrent use. See prom.Mirror for a ready, concurrency-safe hook.
	Observe func(info MirrorInfo)

	// ErrorLog logs a recovered mirror panic; nil uses the standard logger.
	ErrorLog *log.Logger

	// MarkHeader/MarkValue stamp the mirrored request so the canary can detect it and
	// no-op side effects. Default "X-Mirror"/"1". Set DisableMark to turn marking off.
	MarkHeader string
	MarkValue  string

	// SampleRate is the fraction in [0,1] of MATCHED requests to mirror. The zero
	// value means 1.0 (mirror all matched) — disable mirroring by NOT installing the
	// middleware, not by SampleRate=0. Values > 1 clamp to 1; negative clamps to 1.
	SampleRate float64

	// Timeout bounds a mirror's total in-system lifetime — queue wait plus the
	// round-trip — as one deadline set at dispatch. It is cooperative: the destination
	// chain must honor the request context (upstream/httputil do). Default 10s.
	Timeout time.Duration

	// MaxBodyBytes caps the request body buffered per mirror; a larger body skips the
	// mirror (the primary is unaffected). Default 1<<20.
	MaxBodyBytes int64

	// Workers is the fixed number of dispatch goroutines (the real concurrency cap).
	// Default 8.
	Workers int

	// QueueSize is the buffered job-queue depth; a full queue drops (never blocks the
	// primary). Default 256.
	QueueSize int

	// DisableBody, when true, always mirrors with no body (http.NoBody) — the
	// zero-buffer mode for large-payload services. The zero value mirrors bodies.
	DisableBody bool

	// DisableMark, when true, omits the MarkHeader stamp. The zero value marks (so a
	// canary can detect shadow traffic by default); set it for a fully transparent
	// mirror.
	DisableMark bool
}

// Use appends a middleware to the mirror destination chain. SETUP ONLY: it must be
// called before the first request; the chain is frozen on first ServeHandler and a
// later Use is silently ignored.
func (m *Mirror) Use(x parapet.Middleware) { m.ms.Use(x) }

// UseFunc appends a middleware func to the mirror destination chain. SETUP ONLY (see
// Use).
func (m *Mirror) UseFunc(x parapet.MiddlewareFunc) { m.ms.UseFunc(x) }

func (m *Mirror) init() {
	if m.SampleRate <= 0 {
		m.SampleRate = 1 // zero/unset/negative => mirror all matched; disable by not installing
	}
	if m.SampleRate > 1 {
		m.SampleRate = 1
	}
	if m.MaxBodyBytes <= 0 {
		m.MaxBodyBytes = 1 << 20
	}
	if m.Timeout <= 0 {
		m.Timeout = 10 * time.Second
	}
	if m.Workers <= 0 {
		m.Workers = 8
	}
	if m.QueueSize <= 0 {
		m.QueueSize = 256
	}
	if m.MarkHeader == "" {
		m.MarkHeader = "X-Mirror"
	}
	if m.MarkValue == "" {
		m.MarkValue = "1"
	}
	// Materialize the destination chain once. NotFoundHandler is the terminal, so a
	// chain with no upstream just 404s into the discard writer (harmless).
	m.handler = m.ms.ServeHandler(http.NotFoundHandler())
	m.jobs = make(chan *mirrorJob, m.QueueSize)
	for range m.Workers {
		go m.worker()
	}
}

func (m *Mirror) logf(format string, v ...any) {
	if m.ErrorLog == nil {
		log.Printf(format, v...)
		return
	}
	m.ErrorLog.Printf(format, v...)
}

func (m *Mirror) emit(info MirrorInfo) {
	if m.Observe != nil {
		m.Observe(info)
	}
}

// ServeHandler implements parapet.Middleware.
func (m *Mirror) ServeHandler(next http.Handler) http.Handler {
	m.once.Do(m.init)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Gate cheapest first; each short-circuits to next with no clone/buffer/goroutine.
		if m.Match != nil && !m.Match(r) {
			next.ServeHTTP(w, r)
			return
		}
		if m.SampleRate < 1 && rand.Float64() >= m.SampleRate {
			next.ServeHTTP(w, r)
			return
		}

		// Capture the body ONCE up front, bounded. captureBody rewires r.Body so the
		// PRIMARY reads identical bytes, and returns the bytes for the mirror (or
		// declines). It MUST run before r is read elsewhere.
		body, ok := m.captureBody(r)
		if !ok {
			// Body over cap (or unreadable): primary already restored; skip the mirror.
			m.dropOversize.Add(1)
			m.emit(MirrorInfo{Outcome: OutcomeDroppedOversize})
			next.ServeHTTP(w, r)
			return
		}

		// Enqueue the detached mirror BEFORE serving the primary: the body and overflow
		// decision are already final, the enqueue is a non-blocking send, and starting
		// the mirror alongside the primary gives the canary realistic timing.
		m.dispatch(r, body)

		next.ServeHTTP(w, r)
	})
}

// captureBody reads the body up to MaxBodyBytes, leaving the PRIMARY able to read the
// identical bytes. It returns:
//
//	(nil,  true)  — bodiless request (or DisableBody): the mirror gets NoBody.
//	(buf,  true)  — body fit the cap: primary rewired to a fresh reader over buf.
//	(nil,  false) — body exceeds the cap or a read error: primary restored, mirror declined.
func (m *Mirror) captureBody(r *http.Request) ([]byte, bool) {
	if m.DisableBody || r.Body == nil || r.Body == http.NoBody {
		return nil, true
	}
	// GetBody (a stdlib-replayable body) lets us read the mirror's copy WITHOUT
	// disturbing the primary's reader — the primary keeps r.Body untouched. It still
	// buffers the mirror copy (MaxBodyBytes+1) on the request goroutine.
	if r.GetBody != nil {
		if rc, err := r.GetBody(); err == nil {
			b, err := io.ReadAll(io.LimitReader(rc, m.MaxBodyBytes+1))
			_ = rc.Close()
			if err == nil && int64(len(b)) <= m.MaxBodyBytes {
				return b, true // primary body untouched; mirror replays b
			}
		}
		return nil, false // oversize or replay failed: decline, primary untouched
	}

	limit := m.MaxBodyBytes
	var br bytes.Buffer
	n, err := io.CopyN(&br, r.Body, limit+1)
	read := br.Bytes()
	if err != nil && err != io.EOF {
		// Read error: restore consumed bytes + remainder so the primary is whole; decline.
		r.Body = restoreBody(read, r.Body)
		return nil, false
	}
	if n > limit {
		// Over the cap: primary streams normally from read-bytes + untouched remainder.
		r.Body = restoreBody(read, r.Body)
		return nil, false
	}
	_ = r.Body.Close()
	// Primary now reads the buffered copy; identical bytes, Content-Length preserved.
	// Copy out of the buffer so the mirror's view cannot be aliased by buffer reuse.
	out := append([]byte(nil), read...)
	r.Body = io.NopCloser(bytes.NewReader(out))
	return out, true
}

// restoreBody splices already-read bytes ahead of the untouched remainder, preserving
// Close on the original body. Used when we decline to mirror but must leave the
// primary able to read the whole body.
func restoreBody(read []byte, rest io.ReadCloser) io.ReadCloser {
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(read), rest), rest}
}

// dispatch builds a detached, header-sanitized, marked mirror request and enqueues it
// without ever blocking the primary.
func (m *Mirror) dispatch(r *http.Request, body []byte) {
	// FRESH root context, NOT WithoutCancel(r.Context()): the mirror outlives the
	// client request and runs concurrently with its teardown, so it must not share
	// request-scoped mutable state (logger record, trace span) nor be cancelled when
	// the client response completes — exactly as pkg/cache/stale.go argues.
	ctx, cancel := context.WithTimeout(context.Background(), m.Timeout)

	c := r.Clone(ctx) // deep-copies Header/URL/Trailer/Form; mirror mutations are isolated
	c.RequestURI = "" // must be empty on a client request (else RoundTrip panics)
	c.RemoteAddr = "" // don't forward, like upstream.go's r.RemoteAddr = ""
	c.Close = false
	c.TransferEncoding = nil // else inbound chunked framing overrides our fixed Content-Length
	for k := range c.Header {
		if _, hop := hopByHop[http.CanonicalHeaderKey(k)]; hop {
			delete(c.Header, k)
		}
	}
	c.Header.Del("Expect") // don't negotiate 100-continue with the canary
	if !m.DisableMark {
		c.Header.Set(m.MarkHeader, m.MarkValue) // canary can detect and no-op side effects
	}
	if body != nil {
		c.Body = io.NopCloser(bytes.NewReader(body)) // own reader over shared immutable bytes
		c.ContentLength = int64(len(body))
		c.GetBody = func() (io.ReadCloser, error) { // lets the proxy retry an idempotent mirror
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	} else {
		c.Body = http.NoBody
		c.ContentLength = 0
		c.GetBody = func() (io.ReadCloser, error) { return http.NoBody, nil }
	}

	job := &mirrorJob{req: c, cancel: cancel}
	select {
	case m.jobs <- job:
		m.dispatched.Add(1)
		m.emit(MirrorInfo{Outcome: OutcomeDispatched})
	default:
		// Queue full: DROP, never block the primary. Release the context immediately so
		// its timer does not leak.
		cancel()
		m.dropFull.Add(1)
		m.emit(MirrorInfo{Outcome: OutcomeDroppedFull})
	}
}

func (m *Mirror) worker() {
	for job := range m.jobs {
		m.serveOne(job)
	}
}

// serveOne drives one mirror request and discards its response. recover() is
// MANDATORY: this goroutine runs outside the http.Server's per-request recover, so a
// mirror panic would otherwise crash the whole proxy.
func (m *Mirror) serveOne(job *mirrorJob) {
	defer job.cancel() // release the detached context (frees the timer) on completion
	start := time.Now()
	dw := &discardResponseWriter{}
	defer func() {
		if rec := recover(); rec != nil {
			m.panicked.Add(1)
			m.logf("mirror: recovered panic: %v", rec)
			m.emit(MirrorInfo{Outcome: OutcomePanicked})
		}
	}()
	m.handler.ServeHTTP(dw, job.req)
	m.completed.Add(1)
	m.emit(MirrorInfo{Outcome: OutcomeCompleted, Status: dw.status, Duration: time.Since(start)})
}

// Stats returns the lock-free dispatch counters for tests/introspection.
func (m *Mirror) Stats() (dispatched, dropFull, dropOversize, completed, panicked uint64) {
	return m.dispatched.Load(), m.dropFull.Load(), m.dropOversize.Load(),
		m.completed.Load(), m.panicked.Load()
}

// discardResponseWriter swallows the mirror's response (status/headers/body), like
// cache/stale.go's discardResponseWriter on a background revalidation. Header() is
// lazily stable so the proxy can set headers safely; Write drops the body; a no-op
// Flush satisfies httputil.ReverseProxy's http.Flusher type-assert on streaming
// responses.
type discardResponseWriter struct {
	h      http.Header
	status int
}

func (d *discardResponseWriter) Header() http.Header {
	if d.h == nil {
		d.h = http.Header{}
	}
	return d.h
}

func (d *discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d *discardResponseWriter) WriteHeader(code int)        { d.status = code }
func (d *discardResponseWriter) Flush()                      {}
