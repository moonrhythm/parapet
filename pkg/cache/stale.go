package cache

import (
	"context"
	"net/http"
	"time"
)

// freshState classifies a stored entry at serve time.
type freshState int

const (
	stateExpired         freshState = iota // past every window (or invalidated): not serveable
	stateFresh                             // within FreshUntil: serve as a normal HIT
	stateStaleRevalidate                   // stale, within stale-while-revalidate: serve stale + refresh
	stateStaleIfError                      // stale, within stale-if-error only: serve stale on a failed revalidation
)

// classify decides how a stored entry may be served now: fresh until FreshUntil,
// then — if the origin offered RFC 5861 windows — serveable stale within
// stale-while-revalidate, then serveable on error within stale-if-error, then
// fully expired. Out-of-band invalidation (InvalidatedAfter) overrides any
// serveable state and forces stateExpired, so a purged entry is never served —
// fresh or stale. The hook is consulted only for an entry that would otherwise be
// served, so a time-expired entry is reaped without paying for it.
func (c *Cache) classify(m Meta, r *http.Request, now time.Time) freshState {
	fresh := time.Unix(0, m.FreshUntil)
	var state freshState
	switch {
	case !now.After(fresh):
		state = stateFresh
	case m.StaleWhileRevalidate > 0 && now.Before(fresh.Add(time.Duration(m.StaleWhileRevalidate))):
		state = stateStaleRevalidate
	case m.StaleIfError > 0 && now.Before(fresh.Add(time.Duration(m.StaleIfError))):
		state = stateStaleIfError
	default:
		return stateExpired
	}
	if c.invalidatedAfter != nil && m.Created <= c.invalidatedAfter(r, m) {
		return stateExpired
	}
	return state
}

// serveableUntil is the last instant an entry may be served: FreshUntil plus the
// larger of its RFC 5861 windows. Past it the entry is fully expired and reapable.
// classify and the disk startup scan share this bound so a stale-but-serveable
// entry is never reaped out from under stale-if-error.
func (m Meta) serveableUntil() time.Time {
	window := m.StaleWhileRevalidate
	if m.StaleIfError > window {
		window = m.StaleIfError
	}
	return time.Unix(0, m.FreshUntil).Add(time.Duration(window))
}

// revalidate refreshes a variant in the background (stale-while-revalidate). It
// is single-flighted through the same fill lock as a foreground fill: if a fill
// or another revalidation is already in flight for this variant, it does
// nothing. The fetch runs on a detached, time-bounded context so it is not
// cancelled when the triggering client's response completes, and a hung origin
// can't pin the lock or leak the goroutine. The response is streamed only to
// storage (a discard client writer), reusing the normal store path.
func (c *Cache) revalidate(r *http.Request, next http.Handler, primaryHex string) {
	variantHex := c.variantHash(primaryHex, r)
	lock, leader := c.acquire(variantHex)
	if !leader {
		return
	}

	// Detach from the request entirely: a fresh context, NOT WithoutCancel of
	// r.Context(). The background fetch outlives the client request and runs
	// concurrently with its teardown, so it must not share request-scoped mutable
	// state — e.g. the logger's per-request record, which the original request is
	// finalizing as this goroutine writes to it. It also must not pollute that
	// request's log/trace with the revalidation's own fields.
	ctx, cancel := context.WithTimeout(context.Background(), c.revalidateTimeout)
	rr := r.Clone(ctx)
	rr.Body = http.NoBody // GET/HEAD carry no body; never touch the original

	go func() {
		defer cancel()
		// This goroutine runs outside the http.Server's per-request recover, so a
		// panic in next would crash the process. Contain it: a failed refresh just
		// leaves the stale entry for the next request to retry.
		defer func() { _ = recover() }()
		defer c.release(variantHex, lock)

		// lock is left nil on the teeWriter so DecoupleFill never engages: there is
		// no real client to isolate, only the discard writer.
		tw := &teeWriter{rw: &discardResponseWriter{}, r: rr, c: c, method: rr.Method, primaryHex: primaryHex}
		defer tw.cleanup()
		next.ServeHTTP(tw, rr)
		tw.finish()
	}()
}

// fillWithStale serves a miss for a stale-if-error-eligible entry: it runs the
// normal fill (so single-flight, Vary learning, and caching all apply) but routes
// the client write through a staleGate. If the origin's revalidation produces a
// server error (status >= 500), the gate suppresses it and the stale entry is
// served instead. It returns the request's cache outcome: ResultStaleError when it
// fell back to the stale entry (carrying the failed fetch's duration), otherwise
// the inner fill's own result (a MISS that successfully revalidated, or a HIT
// served from a concurrent leader's fill through the gate).
func (c *Cache) fillWithStale(w http.ResponseWriter, r *http.Request, next http.Handler, primaryHex string, m Meta, body []byte) ResultInfo {
	// gate.m points at this stack-local m; finalize runs synchronously below
	// (before this frame returns), so the pointer never escapes. fillAndServe and
	// the teeWriter it builds keep the gate stack-local and do not retain it.
	gate := &staleGate{rw: w, r: r, m: &m, body: body}
	info := c.fillAndServe(gate, r, next, primaryHex)
	gate.finalize()
	if gate.fellBack {
		return ResultInfo{Result: ResultStaleError, FillDuration: info.FillDuration}
	}
	return info
}

// staleGate wraps the client ResponseWriter during a stale-if-error fill. It
// passes a normal (status < 500) response straight through; on the first
// server-error status it suppresses the origin's output and remembers to serve
// the stale entry from finalize instead. Only the response headers are gated, so
// a successful revalidation streams with no added latency.
type staleGate struct {
	rw       http.ResponseWriter
	r        *http.Request
	m        *Meta
	body     []byte
	decided  bool
	fellBack bool
}

func (g *staleGate) Header() http.Header { return g.rw.Header() }

func (g *staleGate) WriteHeader(code int) {
	if g.decided {
		return
	}
	g.decided = true
	if code >= 500 {
		g.fellBack = true
		return
	}
	g.rw.WriteHeader(code)
}

func (g *staleGate) Write(p []byte) (int, error) {
	if !g.decided {
		g.WriteHeader(http.StatusOK)
	}
	if g.fellBack {
		return len(p), nil // swallow the origin's error body
	}
	return g.rw.Write(p)
}

// finalize serves the stale entry when the origin errored. Nothing has been
// written to the client yet (the error status/body were suppressed), so the
// origin's headers are cleared before writing the stored response.
func (g *staleGate) finalize() {
	if !g.fellBack {
		return
	}
	h := g.rw.Header()
	for k := range h {
		delete(h, k)
	}
	writeStored(g.rw, g.r, *g.m, g.body, "STALE")
}

func (g *staleGate) Flush() {
	if g.fellBack {
		return
	}
	if f, ok := g.rw.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (g *staleGate) Unwrap() http.ResponseWriter { return g.rw }

// discardResponseWriter is the client side of a background revalidation: the
// response body is streamed to storage by the teeWriter, never to a client, so
// every write here is dropped. Its Header map is stable across calls (lazily
// created) so the origin's response headers reach the teeWriter's cacheability
// decision.
type discardResponseWriter struct{ h http.Header }

func (d *discardResponseWriter) Header() http.Header {
	if d.h == nil {
		d.h = http.Header{}
	}
	return d.h
}

func (d *discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }

func (*discardResponseWriter) WriteHeader(int) {}
