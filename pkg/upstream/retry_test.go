package upstream_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/moonrhythm/parapet/pkg/upstream"
)

// bodyRecorder is a transport that fails every attempt (forcing the retry loop to
// re-serve) while recording the FULL body bytes it received on each attempt. It is
// the fake-upstream used to prove a retried body-bearing request is rewound to the
// complete body — not an empty/consumed one — on every attempt.
type bodyRecorder struct {
	mu      sync.Mutex
	bodies  [][]byte // one entry per attempt, in order
	failAll bool     // when true, every attempt returns a transport error
}

func (t *bodyRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	var b []byte
	if r.Body != nil {
		b, _ = io.ReadAll(r.Body) // drain like a real transport would
		_ = r.Body.Close()
	}
	t.mu.Lock()
	t.bodies = append(t.bodies, b)
	t.mu.Unlock()
	if t.failAll {
		return nil, fmt.Errorf("can not dial to server")
	}
	return httptest.NewRecorder().Result(), nil
}

func (t *bodyRecorder) recorded() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([][]byte, len(t.bodies))
	copy(out, t.bodies)
	return out
}

// fastBackoff is a small backoff so retry tests stay quick yet still exercise the
// real exponential-backoff timer path.
const fastBackoff = time.Millisecond

// TestRetry_DefaultBodylessGetUnchanged proves nil RetryPolicy + a body-less GET
// retries exactly as today: 1 initial attempt + Retries re-attempts.
func TestRetry_DefaultBodylessGetUnchanged(t *testing.T) {
	t.Parallel()

	tr := &bodyRecorder{failAll: true}
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	Upstream{
		Transport:     tr,
		Retries:       3,
		BackoffFactor: fastBackoff,
	}.ServeHandler(nil).ServeHTTP(w, r)

	assert.Len(t, tr.recorded(), 4, "1 initial + 3 retries (default body-less behavior unchanged)")
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

// TestRetry_CustomPolicyRetriesPUT proves a custom RetryPolicy allowing PUT actually
// retries a PUT (the default canRetry would reject it).
func TestRetry_CustomPolicyRetriesPUT(t *testing.T) {
	t.Parallel()

	tr := &bodyRecorder{failAll: true}
	// PUT body-less here to isolate the policy decision from body rewinding.
	r := httptest.NewRequest("PUT", "/", nil)
	w := httptest.NewRecorder()
	Upstream{
		Transport:     tr,
		Retries:       3,
		BackoffFactor: fastBackoff,
		RetryPolicy: func(r *http.Request) bool {
			return r.Method == http.MethodPut // operator opts PUT in
		},
	}.ServeHandler(nil).ServeHTTP(w, r)

	assert.Len(t, tr.recorded(), 4, "custom RetryPolicy makes a PUT retry: 1 + 3")
}

// TestRetry_BodyBearingWithGetBodyGetsFullBody proves a body-bearing request whose
// GetBody is set is retried AND each attempt receives the FULL body (not an empty,
// already-consumed one). This is the rewind correctness guard.
func TestRetry_BodyBearingWithGetBodyGetsFullBody(t *testing.T) {
	t.Parallel()

	const payload = "hello-rewindable-world"
	tr := &bodyRecorder{failAll: true}
	// Install Body + GetBody + ContentLength explicitly, exactly as mirror.dispatch
	// does (httptest's server-side constructor sets neither GetBody nor a matching
	// ContentLength; and httputil.ReverseProxy nils a body whose ContentLength is 0).
	r := httptest.NewRequest("GET", "/", nil)
	r.Body = io.NopCloser(strings.NewReader(payload))
	r.ContentLength = int64(len(payload))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(payload)), nil
	}
	require.NotNil(t, r.GetBody, "the rewind hook must be present for this test")
	w := httptest.NewRecorder()
	Upstream{
		Transport:     tr,
		Retries:       3,
		BackoffFactor: fastBackoff,
	}.ServeHandler(nil).ServeHTTP(w, r)

	got := tr.recorded()
	require.Len(t, got, 4, "a rewindable body-bearing GET retries: 1 + 3")
	for i, b := range got {
		assert.Equal(t, payload, string(b), "attempt %d must receive the FULL body, not a consumed one", i)
	}
}

// TestRetry_BodyWithoutGetBodyNotRetried proves a request WITH a body but WITHOUT
// GetBody is NOT retried (unchanged from today's body-less-only rule).
func TestRetry_BodyWithoutGetBodyNotRetried(t *testing.T) {
	t.Parallel()

	const payload = "payload"
	tr := &bodyRecorder{failAll: true}
	// A body WITH a matching ContentLength (so the proxy keeps it) but NO GetBody:
	// not rewindable, so the default policy must refuse to retry it.
	r := httptest.NewRequest("GET", "/", nil)
	r.Body = io.NopCloser(strings.NewReader(payload))
	r.ContentLength = int64(len(payload))
	r.GetBody = nil
	w := httptest.NewRecorder()
	Upstream{
		Transport:     tr,
		Retries:       3,
		BackoffFactor: fastBackoff,
	}.ServeHandler(nil).ServeHTTP(w, r)

	assert.Len(t, tr.recorded(), 1, "a body with no GetBody is not rewindable -> not retried")
}

// TestRetry_GetBodyErrorStopsRetry proves that if the GetBody rewind itself fails on
// a re-attempt, the loop gives up retrying and surfaces the original transport error
// (502) instead of looping or re-sending an empty body. The first attempt runs; the
// rewind for attempt 2 errors -> no further attempts.
func TestRetry_GetBodyErrorStopsRetry(t *testing.T) {
	t.Parallel()

	const payload = "payload"
	tr := &bodyRecorder{failAll: true}
	r := httptest.NewRequest("GET", "/", nil)
	r.Body = io.NopCloser(strings.NewReader(payload))
	r.ContentLength = int64(len(payload))
	var calls atomic.Int64
	r.GetBody = func() (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			return nil, fmt.Errorf("rewind boom") // fail the first rewind (for attempt 2)
		}
		return io.NopCloser(strings.NewReader(payload)), nil
	}
	w := httptest.NewRecorder()
	Upstream{
		Transport:     tr,
		Retries:       3,
		BackoffFactor: fastBackoff,
	}.ServeHandler(nil).ServeHTTP(w, r)

	assert.Len(t, tr.recorded(), 1, "a failed rewind stops retrying after the first attempt")
	assert.Equal(t, http.StatusBadGateway, w.Code, "the original transport error is surfaced")
}

// countingFake counts calls and honors context cancellation, like ctxFake but in the
// external test package. Used to assert the hedging leg count.
type countingFake struct {
	calls atomic.Int64
	delay time.Duration
}

func (t *countingFake) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	if t.delay > 0 {
		select {
		case <-time.After(t.delay):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}
	return httptest.NewRecorder().Result(), nil
}

func hedgeLB(trs ...http.RoundTripper) *RoundRobinLoadBalancer {
	targets := make([]*Target, len(trs))
	for i, tr := range trs {
		targets[i] = &Target{Host: fmt.Sprintf("t%d", i), Transport: tr}
	}
	return NewRoundRobinLoadBalancer(targets)
}

// TestRetry_HedgingDoesNotHedgeBodyBearing is the OPTION (b) correctness guard: a
// body-bearing request (even one carrying a rewindable GetBody) is NOT hedged, so no
// second concurrent leg can share — and consume to empty — the single shared Body.
// The primary leg is slow so a hedge WOULD fire if the request were eligible; we
// assert it never does.
func TestRetry_HedgingDoesNotHedgeBodyBearing(t *testing.T) {
	t.Parallel()

	primary := &countingFake{delay: 200 * time.Millisecond} // would lose to a hedge if one fired
	hedge := &countingFake{}
	h := NewHedgingLoadBalancer(hedgeLB(primary, hedge))
	h.HedgeDelay = 5 * time.Millisecond // a hedge would fire well before the primary finishes

	// A GET with a rewindable body (GetBody set, as mirror.dispatch installs):
	// eligible for the Upstream retry path — but body-bearing, so deliberately NOT
	// hedgeable.
	r := httptest.NewRequest("GET", "/", nil)
	r.Body = io.NopCloser(bytes.NewReader([]byte("payload")))
	r.ContentLength = int64(len("payload"))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader([]byte("payload"))), nil
	}
	require.NotNil(t, r.GetBody)
	resp, err := h.RoundTrip(r)
	require.NoError(t, err)
	resp.Body.Close()

	assert.EqualValues(t, 1, primary.calls.Load(), "the single (primary) leg served the body-bearing request")
	assert.EqualValues(t, 0, hedge.calls.Load(), "a body-bearing request is NEVER hedged (option b)")
}

// TestRetry_HedgingStillHedgesBodylessGet confirms the option (b) change did not
// disable hedging for the body-LESS GET it has always supported.
func TestRetry_HedgingStillHedgesBodylessGet(t *testing.T) {
	t.Parallel()

	slow := &countingFake{delay: time.Second} // primary loses
	fast := &countingFake{}                    // hedge wins
	h := NewHedgingLoadBalancer(hedgeLB(slow, fast))
	h.HedgeDelay = 10 * time.Millisecond

	resp, err := h.RoundTrip(httptest.NewRequest("GET", "/", nil))
	require.NoError(t, err)
	resp.Body.Close()

	assert.EqualValues(t, 1, slow.calls.Load(), "primary leg ran")
	assert.EqualValues(t, 1, fast.calls.Load(), "the body-less GET was hedged as before")
}
