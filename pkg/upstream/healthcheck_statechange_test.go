package upstream

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// downPt builds a probeTarget whose gate bit starts up (the common case for a
// down-crossing test) or down, for driving observe()/probe() directly.
func hcPeer(host string, up bool) *probeTarget {
	var b atomic.Bool
	b.Store(up)
	return &probeTarget{target: &Target{Host: host}, up: &b}
}

func TestActiveHealthCheck_OnStateChange_DownCrossing(t *testing.T) {
	t.Parallel()
	var rec stateRecorder
	a := &ActiveHealthCheck{UnhealthyThld: 2, HealthyThld: 1, OnStateChange: rec.fn()}
	pt := hcPeer("h", true)

	a.observe(pt, false, CauseTimeout) // failRun 1 < 2: sub-threshold, no event
	assert.Empty(t, rec.all(), "no event before UnhealthyThld is reached")
	assert.True(t, pt.up.Load())

	a.observe(pt, false, CauseTimeout) // failRun 2 == 2: crosses
	require.Equal(t, 1, rec.count(ReasonProbeDown))
	c := rec.all()[0]
	assert.Equal(t, "h", c.Host)
	assert.Equal(t, StateClosed, c.From)
	assert.Equal(t, StateOpen, c.To)
	assert.Equal(t, CauseTimeout, c.Cause, "the down event carries the classified cause")
	assert.False(t, pt.up.Load())

	a.observe(pt, false, CauseRefused) // already down: the && up.Load() guard blocks a duplicate
	assert.Equal(t, 1, rec.count(ReasonProbeDown), "no duplicate down event while already down")
}

func TestActiveHealthCheck_OnStateChange_RecoverCrossing(t *testing.T) {
	t.Parallel()
	var rec stateRecorder
	a := &ActiveHealthCheck{UnhealthyThld: 1, HealthyThld: 2, OnStateChange: rec.fn()}
	pt := hcPeer("h", false)

	a.observe(pt, true, CauseNone) // okRun 1 < 2: sub-threshold
	assert.Empty(t, rec.all(), "no event before HealthyThld is reached")
	assert.False(t, pt.up.Load())

	a.observe(pt, true, CauseNone) // okRun 2 == 2: crosses
	require.Equal(t, 1, rec.count(ReasonProbeRecover))
	c := rec.all()[0]
	assert.Equal(t, StateOpen, c.From)
	assert.Equal(t, StateClosed, c.To)
	assert.Equal(t, CauseNone, c.Cause, "a recover carries no cause")
	assert.True(t, pt.up.Load())

	a.observe(pt, true, CauseNone)
	assert.Equal(t, 1, rec.count(ReasonProbeRecover), "no duplicate recover event while already up")
}

func TestActiveHealthCheck_OnStateChange_NilHookNoPanic(t *testing.T) {
	t.Parallel()
	a := &ActiveHealthCheck{UnhealthyThld: 1, HealthyThld: 1} // OnStateChange nil
	pt := hcPeer("h", true)
	assert.NotPanics(t, func() {
		a.observe(pt, false, CauseError) // down crossing, nil hook
		a.observe(pt, true, CauseNone)   // recover crossing, nil hook
	})
	assert.True(t, pt.up.Load(), "the gate still flips with a nil hook")
}

func TestActiveHealthCheck_OnStateChange_NoEventAtInit(t *testing.T) {
	t.Parallel()
	for _, startUnhealthy := range []bool{false, true} {
		var rec stateRecorder
		a := &ActiveHealthCheck{
			Targets:        []*Target{{Host: "t", Transport: freshBody()}},
			StartUnhealthy: startUnhealthy,
			OnStateChange:  rec.fn(),
		}
		a.init() // builds the initial gate; an initial state is not a transition
		assert.Empty(t, rec.all(), "init fires no event (StartUnhealthy=%v)", startUnhealthy)
	}
}

func TestActiveHealthCheck_OnStateChange_StartUnhealthyFirstRecover(t *testing.T) {
	t.Parallel()
	var rec stateRecorder
	a := &ActiveHealthCheck{HealthyThld: 1, UnhealthyThld: 3, OnStateChange: rec.fn()}
	pt := hcPeer("t", false) // StartUnhealthy => begins down

	a.observe(pt, true, CauseNone) // the first admitting probe
	assert.Equal(t, 1, rec.count(ReasonProbeRecover), "cold-start admission emits exactly one recover")
	assert.True(t, pt.up.Load())
}

func TestClassifyProbeCause(t *testing.T) {
	t.Parallel()

	// A real url.Error -> net.OpError -> os.SyscallError -> syscall.Errno chain, the
	// shape an http.Transport actually returns, to prove errors.Is/As traverse it.
	wrappedRefused := &url.Error{Op: "Get", URL: "http://x", Err: &net.OpError{
		Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED),
	}}

	cases := []struct {
		name string
		resp *http.Response
		err  error
		want ProbeCause
	}{
		{"no-error-unhealthy", &http.Response{StatusCode: 503}, nil, CauseStatus},
		{"deadline", nil, context.DeadlineExceeded, CauseTimeout},
		{"deadline-wrapped", nil, fmt.Errorf("probe: %w", context.DeadlineExceeded), CauseTimeout},
		{"dns-nxdomain", nil, &net.DNSError{Err: "no such host"}, CauseDNS},
		// The real shape a resolver returns on a lookup timeout: a *net.DNSError that
		// WRAPS context.DeadlineExceeded (Go 1.23+ DNSError.Unwrap). It must classify as
		// dns, not timeout — the DNSError branch precedes the deadline branch. (The old
		// synthetic {IsTimeout:true} with a nil UnwrapErr did not wrap a deadline, so it
		// never exercised this ordering and gave false confidence.)
		{"dns-timeout-wraps-deadline", nil, &net.DNSError{Err: "timeout", IsTimeout: true, UnwrapErr: context.DeadlineExceeded}, CauseDNS},
		{"refused", nil, syscall.ECONNREFUSED, CauseRefused},
		{"refused-wrapped", nil, wrappedRefused, CauseRefused},
		{"reset", nil, syscall.ECONNRESET, CauseReset},
		{"eof", nil, io.EOF, CauseReset},
		{"unexpected-eof", nil, io.ErrUnexpectedEOF, CauseReset},
		{"tls-record", nil, tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, CauseTLS},
		{"tls-cert", nil, &tls.CertificateVerificationError{}, CauseTLS},
		{"net-timeout-no-deadline", nil, timeoutOnlyErr{}, CauseTimeout},
		{"other", nil, errors.New("boom"), CauseError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classifyProbeCause(tc.resp, tc.err))
		})
	}
}

// timeoutOnlyErr is a net.Error reporting a timeout WITHOUT wrapping
// context.DeadlineExceeded — e.g. a transport dial i/o timeout.
type timeoutOnlyErr struct{}

func (timeoutOnlyErr) Error() string   { return "i/o timeout" }
func (timeoutOnlyErr) Timeout() bool   { return true }
func (timeoutOnlyErr) Temporary() bool { return false }

func TestActiveHealthCheck_ProbeSuppressedOnParentCancel(t *testing.T) {
	t.Parallel()
	// A probe in flight when the prober's PARENT ctx is cancelled (Close / graceful
	// shutdown) returns context.Canceled; that must NOT darken the gate or emit a
	// spurious ReasonProbeDown. delay forces the probe to block until ctx is done.
	var rec stateRecorder
	a := &ActiveHealthCheck{
		Path: "/hz", Method: "GET", Scheme: "http", Timeout: time.Second,
		UnhealthyThld: 1, OnStateChange: rec.fn(),
	}
	pt := hcPeer("t", true)
	pt.target.Transport = &healthFake{healthPath: "/hz", delay: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // shutting down before the probe runs

	a.probe(ctx, pt) // synchronous: RoundTrip returns Canceled via the cancelled pctx

	assert.Empty(t, rec.all(), "a shutdown-cancelled probe emits no event")
	assert.True(t, pt.up.Load(), "a shutdown-cancelled probe does not darken the gate")
}

func TestActiveHealthCheck_ProbeTimeoutClassifiesTimeout(t *testing.T) {
	t.Parallel()
	// A genuine per-probe Timeout (parent ctx alive, only pctx expired) is a real
	// failure classified as timeout — distinct from the shutdown-cancel above.
	var rec stateRecorder
	a := &ActiveHealthCheck{
		Path: "/hz", Method: "GET", Scheme: "http", Timeout: 20 * time.Millisecond,
		UnhealthyThld: 1, OnStateChange: rec.fn(),
	}
	pt := hcPeer("t", true)
	pt.target.Transport = &healthFake{healthPath: "/hz", delay: 200 * time.Millisecond}

	a.probe(context.Background(), pt) // parent alive; pctx deadline fires at 20ms

	require.Equal(t, 1, rec.count(ReasonProbeDown))
	assert.Equal(t, CauseTimeout, rec.all()[0].Cause, "a per-probe timeout classifies as timeout, not suppressed")
	assert.False(t, pt.up.Load())
}
