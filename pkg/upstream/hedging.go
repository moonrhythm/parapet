package upstream

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

const defaultMaxHedge = 1

// loserDrainCap bounds how much of a losing response body is discarded before
// Close, so reaping a large already-headers-received loser cannot tie up a
// goroutine streaming a body nobody will read. A capped-then-closed HTTP/1 body may
// forfeit keep-alive reuse for that one conn, which is fine: the losing leg's
// round-trip has already been context-cancelled.
const loserDrainCap = 4 << 10 // 4 KiB

// NewHedgingLoadBalancer wraps a load balancer (or any http.RoundTripper that picks
// a target per call) with speculative-retry hedging to cut tail latency. HedgeDelay
// is left 0, so the returned wrapper is a zero-cost pass-through until you set it.
func NewHedgingLoadBalancer(next http.RoundTripper) *HedgingLoadBalancer {
	return &HedgingLoadBalancer{Next: next, MaxHedge: defaultMaxHedge, HedgeOnError: true}
}

// HedgingLoadBalancer races an idempotent, body-less request against up to MaxHedge
// hedges, returns the first response to win, and cancels the losing legs. After
// HedgeDelay the in-flight request is duplicated via a second call to the wrapped
// balancer, which self-selects (and advances past) a target, so the hedge naturally
// lands on a different host. The race happens entirely inside RoundTrip, so the
// proxy only ever sees the single winner — there is no concurrent write to the
// client.
//
// It is a drop-in http.RoundTripper for upstream.New and composes with every
// balancer. Only idempotent body-LESS requests are hedged (GET/HEAD/OPTIONS/TRACE
// with no body); this is deliberately stricter than the Upstream retry path, which
// also retries rewindable (GetBody) body-bearing requests. A hedge launches a
// second concurrent leg, and r.Clone only shallow-copies Body, so two legs would
// share one reader — hedging a body-bearing request without per-leg GetBody rewind
// would let a leg send a consumed/empty body. A request already inside the proxy's
// retry loop is not additionally hedged — retries and hedges layer, never multiply.
// HedgeDelay <= 0 disables hedging entirely (no timer, no clone, no goroutine).
//
// The wrapped balancer's Next.RoundTrip MUST honor request-context cancellation
// (every transport in this package does): a hedge cancels the losing legs, so a
// transport that ignores cancellation would leak a draining goroutine per hedge.
// For the same reason, if you give the wrapped balancer a custom IsFailure, it MUST
// exclude context.Canceled (the default does) — otherwise every cancelled losing
// leg is counted as a failure and slowly ejects/trips the healthy backend it raced.
//
// Configuration fields are read once; set them before serving.
//
//nolint:govet // fields grouped by role (state, then config) for readability
type HedgingLoadBalancer struct {
	once sync.Once

	// Next is the wrapped balancer. Each attempt calls Next.RoundTrip, which picks
	// and advances past a target, so a hedge lands on a different one.
	Next http.RoundTripper

	// HedgeDelay is how long to wait for the in-flight request before launching a
	// hedge. <= 0 disables hedging (pure pass-through to Next.RoundTrip).
	HedgeDelay time.Duration

	// MaxHedge is the number of extra speculative attempts beyond the original (each
	// HedgeDelay-spaced). Defaults to 1 (at most one hedge -> 2x fan-out).
	MaxHedge int

	// HedgeOnError launches the next hedge immediately on a losing transport error
	// rather than waiting out HedgeDelay. NewHedgingLoadBalancer enables it; a bare
	// HedgingLoadBalancer{} literal leaves it false (a bool can't be defaulted in init).
	HedgeOnError bool

	// IsHedgeable decides whether a request may be hedged; nil uses the idempotent
	// body-less rule (and never hedges a request already being retried).
	IsHedgeable func(r *http.Request) bool

	// IsWinner decides whether a leg's result wins the race; nil means "a response
	// with no transport error". Set it to, say, only accept non-5xx.
	IsWinner func(resp *http.Response, err error) bool
}

// hedgeResult is one leg's outcome. idx identifies the leg's cancel in the
// supervisor's slice; host is the target it resolved to.
type hedgeResult struct {
	resp *http.Response
	err  error
	host string
	idx  int
}

func (l *HedgingLoadBalancer) init() {
	if l.MaxHedge <= 0 {
		l.MaxHedge = defaultMaxHedge
	}
}

func (l *HedgingLoadBalancer) hedgeable(r *http.Request) bool {
	if l.IsHedgeable != nil {
		return l.IsHedgeable(r)
	}
	// Hedging is body-LESS by default, deliberately STRICTER than the Upstream
	// retry path's canRetry. A hedge launches a SECOND concurrent leg; r.Clone
	// only shallow-copies Body, so two legs would share — and race/consume — one
	// reader. Rewinding via GetBody per leg would be needed to hedge a
	// body-bearing request safely; until that is implemented, a request carrying a
	// body (even a rewindable GetBody one) is NOT hedged, so no leg can ever send a
	// consumed/empty body. Set IsHedgeable to override.
	if !canMethodRetry(r.Method) {
		return false
	}
	if r.Body != nil && r.Body != http.NoBody {
		return false // body-bearing: not hedged (see above)
	}
	_, retrying := r.Context().Value(retryContextKey{}).(int)
	return !retrying // a request already inside the retry loop is not re-hedged
}

func (l *HedgingLoadBalancer) won(resp *http.Response, err error) bool {
	if l.IsWinner != nil {
		return l.IsWinner(resp, err)
	}
	return err == nil && resp != nil
}

// RoundTrip races the request against hedges and returns the winner.
func (l *HedgingLoadBalancer) RoundTrip(r *http.Request) (*http.Response, error) {
	l.once.Do(l.init)

	// Fast path: hedging off or request not eligible -> one plain call, no
	// clone/goroutine/timer.
	if l.HedgeDelay <= 0 || l.MaxHedge <= 0 || !l.hedgeable(r) {
		return l.Next.RoundTrip(r)
	}

	maxAttempts := l.MaxHedge + 1
	results := make(chan hedgeResult, maxAttempts) // buffered: a late loser never blocks on send

	// cancels[i] tears down leg i. Each leg runs on its OWN context (child of the
	// request) so the winner's body stays alive after the losers are cancelled.
	// Touched only by this (supervisor) goroutine.
	cancels := make([]context.CancelFunc, 0, maxAttempts)
	launch := func() {
		idx := len(cancels)
		legCtx, legCancel := context.WithCancel(r.Context())
		cancels = append(cancels, legCancel)
		req := r.Clone(legCtx) // deep-copies Header+URL: legs never share a Host or header slice
		go func() {
			resp, err := safeRoundTrip(l.Next, req)
			results <- hedgeResult{resp: resp, err: err, host: req.URL.Host, idx: idx}
		}()
	}

	launched := 1
	launch() // primary, immediately
	timer := time.NewTimer(l.HedgeDelay)
	defer timer.Stop()

	var firstErr error
	received := 0

	for {
		select {
		case res := <-results:
			received++
			if l.won(res.resp, res.err) {
				// WINNER. Cancel every OTHER leg (idempotent if already cancelled) but
				// NOT the winner's — its body context must stay live until the proxy
				// finishes streaming. Drain the cancelled in-flight losers off-thread.
				r.URL.Host = res.host // report the real winning target to OnRoundTrip/logger
				for j := range cancels {
					if j != res.idx {
						cancels[j]()
					}
				}
				go reap(results, launched-received)
				return wrapWinner(res.resp, cancels[res.idx]), nil
			}
			// Loser: a transport error, or IsWinner==false.
			cancels[res.idx]() // release this finished leg's context
			drainClose(res.resp)
			if firstErr == nil {
				firstErr = res.err
			}
			if received == maxAttempts {
				// Every attempt finished without a winner: surface the first error so the
				// Upstream ErrorHandler maps it (ErrUnavailable->503, else ->502).
				if firstErr == nil {
					firstErr = ErrUnavailable
				}
				return nil, firstErr
			}
			// Fail-fast: an attempt errored and a hedge slot remains -> launch now.
			if l.HedgeOnError && launched < maxAttempts {
				stopDrain(timer)
				launch()
				launched++
				if launched < maxAttempts {
					timer.Reset(l.HedgeDelay)
				}
			}

		case <-timer.C:
			if launched < maxAttempts {
				launch()
				launched++
				if launched < maxAttempts {
					timer.Reset(l.HedgeDelay) // remaining hedges fire every HedgeDelay
				}
			}

		case <-r.Context().Done():
			// Client disconnected. Tear down every leg and drain their bodies.
			for j := range cancels {
				cancels[j]()
			}
			go reap(results, launched-received)
			return nil, r.Context().Err()
		}
	}
}

// wrapWinner hands the proxy the winning response, wrapping its body so Close
// releases the winning leg's context exactly when the proxy is done streaming
// (closing the underlying body first, so a stacked leastconn body still does its
// active-- accounting). It preserves io.ReadWriteCloser so the 101-upgrade
// type-assert in httputil.ReverseProxy still succeeds.
func wrapWinner(resp *http.Response, cancel context.CancelFunc) *http.Response {
	if resp.Body == nil {
		cancel() // nothing to keep the context alive for
		return resp
	}
	if rwc, ok := resp.Body.(io.ReadWriteCloser); ok {
		resp.Body = &hedgeRWCBody{ReadWriteCloser: rwc, cancel: cancel}
	} else {
		resp.Body = &hedgeBody{ReadCloser: resp.Body, cancel: cancel}
	}
	return resp
}

type hedgeBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *hedgeBody) Close() error {
	err := b.ReadCloser.Close() // close underlying first (e.g. leastconn active--, conn return)
	b.cancel()                  // then release the winning leg's context (idempotent)
	return err
}

type hedgeRWCBody struct {
	io.ReadWriteCloser
	cancel context.CancelFunc
}

func (b *hedgeRWCBody) Close() error {
	err := b.ReadWriteCloser.Close()
	b.cancel()
	return err
}

// reap drains and closes the bodies of n still-in-flight legs as they abort, off
// the request goroutine. Every launched leg sends exactly once (safeRoundTrip
// guarantees a send even on panic), so the n reads always complete.
func reap(results <-chan hedgeResult, n int) {
	for range n {
		drainClose((<-results).resp)
	}
}

func drainClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.CopyN(io.Discard, resp.Body, loserDrainCap) // bounded: don't stream a body nobody reads
	_ = resp.Body.Close()
}

// safeRoundTrip publishes a recovered transport panic as an error result so a
// misbehaving Next (e.g. a nil Transport) cannot wedge a reaper waiting on a leg
// that never sends. Mirrors the panic-safety discipline in leastconn/circuitbreaker.
func safeRoundTrip(next http.RoundTripper, r *http.Request) (resp *http.Response, err error) {
	defer func() {
		if recover() != nil {
			resp, err = nil, ErrUnavailable
		}
	}()
	return next.RoundTrip(r)
}

// stopDrain stops a timer and drains its channel so a later Reset is race-free.
func stopDrain(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}
