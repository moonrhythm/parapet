package mirror

import (
	"context"
	"net/http"
	"time"
)

// Outcome classifies one mirror decision/result, reported via Observe.
type Outcome uint8

const (
	// OutcomeDispatched: the mirror request was enqueued for a worker.
	OutcomeDispatched Outcome = iota
	// OutcomeCompleted: a worker finished the round-trip (Status and Duration set).
	OutcomeCompleted
	// OutcomeDroppedFull: the queue was full; the mirror was dropped.
	OutcomeDroppedFull
	// OutcomeDroppedOversize: the body exceeded MaxBodyBytes; the mirror was skipped.
	OutcomeDroppedOversize
	// OutcomePanicked: the mirror chain panicked (recovered; the proxy is unaffected).
	OutcomePanicked
)

// MirrorInfo reports one mirror decision/result to Observe.
//
//nolint:govet // fields ordered for readability
type MirrorInfo struct {
	Outcome  Outcome
	Status   int           // set on OutcomeCompleted
	Duration time.Duration // set on OutcomeCompleted
}

// MirrorFunc is the observation-hook shape (mirrors upstream.RoundTripFunc), returned
// by prom.Mirror for wiring into Mirror.Observe.
type MirrorFunc func(info MirrorInfo)

// mirrorJob is one detached mirror request carried through the worker queue. cancel
// releases the request's detached context (and its timer) when the worker finishes or
// the job is dropped.
type mirrorJob struct {
	req    *http.Request
	cancel context.CancelFunc
}
