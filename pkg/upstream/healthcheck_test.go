package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet"
)

// healthFake is a transport that distinguishes probe requests (path == healthPath)
// from data requests (any other path), counting each and returning a configurable
// probe status. delay lets a probe outlive its context (for the timeout test).
type healthFake struct {
	healthPath string
	status     atomic.Int32 // probe status code; 0 => 200
	delay      time.Duration
	probes     atomic.Int64
	serves     atomic.Int64
}

func (f *healthFake) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == f.healthPath {
		f.probes.Add(1)
		if f.delay > 0 {
			select {
			case <-time.After(f.delay):
			case <-r.Context().Done():
				return nil, r.Context().Err() // a timed-out probe behaves like a real one
			}
		}
		code := int(f.status.Load())
		if code == 0 {
			code = 200
		}
		return hcResp(code), nil
	}
	f.serves.Add(1)
	return hcResp(200), nil
}

func hcResp(code int) *http.Response {
	return &http.Response{StatusCode: code, Body: &countingBody{}, Header: http.Header{}}
}

func TestActiveHealthCheck_MarksDownAfterThreshold(t *testing.T) {
	t.Parallel()
	good := &healthFake{healthPath: "/hz"}
	bad := &healthFake{healthPath: "/hz"}
	bad.status.Store(500) // probe returns 5xx -> unhealthy
	targets := []*Target{{Host: "good", Transport: good}, {Host: "bad", Transport: bad}}

	ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
	ahc.Path = "/hz"
	ahc.Interval = 5 * time.Millisecond
	ahc.UnhealthyThld = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ahc.Start(ctx)
	defer ahc.Close()

	require.Eventually(t, func() bool { return !ahc.up[1].Load() }, 2*time.Second, 5*time.Millisecond,
		"the 5xx target is marked down after the threshold")

	good.serves.Store(0)
	bad.serves.Store(0)
	for range 20 {
		resp, err := ahc.RoundTrip(httptest.NewRequest("GET", "/", nil))
		require.NoError(t, err)
		resp.Body.Close()
	}
	assert.EqualValues(t, 0, bad.serves.Load(), "no data traffic routes to the down target")
	assert.EqualValues(t, 20, good.serves.Load(), "all data traffic routes to the healthy target")
	assert.Positive(t, bad.probes.Load(), "the down target was probed via its own (default) transport")
}

func TestActiveHealthCheck_RecoversAfterThreshold(t *testing.T) {
	t.Parallel()
	backend := &healthFake{healthPath: "/hz"}
	backend.status.Store(500) // starts unhealthy
	targets := []*Target{{Host: "good", Transport: &recordingTransport{}}, {Host: "flap", Transport: backend}}

	ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
	ahc.Path = "/hz"
	ahc.Interval = 5 * time.Millisecond
	ahc.HealthyThld = 2
	ahc.UnhealthyThld = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ahc.Start(ctx)
	defer ahc.Close()

	require.Eventually(t, func() bool { return !ahc.up[1].Load() }, 2*time.Second, 5*time.Millisecond, "starts down")
	backend.status.Store(200) // recovers
	require.Eventually(t, func() bool { return ahc.up[1].Load() }, 2*time.Second, 5*time.Millisecond,
		"after HealthyThld good probes the target is readmitted")
}

func TestActiveHealthCheck_FlapResetsOppositeCounter(t *testing.T) {
	t.Parallel()
	a := &ActiveHealthCheck{HealthyThld: 2, UnhealthyThld: 3}
	var up atomic.Bool
	up.Store(true)
	pt := &probeTarget{up: &up}

	for _, ok := range []bool{true, false, true, false, true, false, true} {
		a.observe(pt, ok) // strictly alternating: neither run ever reaches its threshold
	}
	assert.True(t, up.Load(), "an alternating ok/fail sequence never crosses a threshold")

	a.observe(pt, false)
	a.observe(pt, false)
	a.observe(pt, false) // three consecutive failures now
	assert.False(t, up.Load(), "UnhealthyThld consecutive failures mark it down")
}

func TestActiveHealthCheck_StartUnhealthyBeginsDown(t *testing.T) {
	t.Parallel()
	targets := []*Target{{Host: "t", Transport: freshBody()}}
	ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
	ahc.StartUnhealthy = true
	ahc.init() // build state without spawning probers

	assert.False(t, ahc.up[0].Load(), "fail-closed cold start: a target begins down until a probe admits it")
}

func TestActiveHealthCheck_ProbeTimeout(t *testing.T) {
	t.Parallel()
	slow := &healthFake{healthPath: "/hz", delay: 200 * time.Millisecond} // never answers within Timeout
	targets := []*Target{{Host: "slow", Transport: slow}}

	ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
	ahc.Path = "/hz"
	ahc.Interval = 10 * time.Millisecond
	ahc.Timeout = 20 * time.Millisecond
	ahc.UnhealthyThld = 1
	ahc.StartUnhealthy = true // begins down; a timing-out probe must never bring it up
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ahc.Start(ctx)
	defer ahc.Close()

	require.Eventually(t, func() bool { return slow.probes.Load() > 2 }, 2*time.Second, 10*time.Millisecond,
		"the loop keeps probing despite timeouts (a slow probe does not wedge or stack)")
	assert.False(t, ahc.up[0].Load(), "a probe that exceeds Timeout counts as a failure")
}

func TestActiveHealthCheck_ProbeTransportOverride(t *testing.T) {
	t.Parallel()
	data := &healthFake{healthPath: "/hz"}
	probeTr := &healthFake{healthPath: "/hz"}
	targets := []*Target{{Host: "t", Transport: data}}

	ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
	ahc.Path = "/hz"
	ahc.Interval = 5 * time.Millisecond
	ahc.ProbeTransport = probeTr
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ahc.Start(ctx)
	defer ahc.Close()

	require.Eventually(t, func() bool { return probeTr.probes.Load() > 2 }, 2*time.Second, 10*time.Millisecond)
	assert.EqualValues(t, 0, data.probes.Load(), "probes use ProbeTransport, never the data transport, when set")
}

// UnixSocketHostProbes is the regression guard for the unix-socket probe bug: a Host
// containing slashes must be assigned to req.URL.Host directly, never round-tripped
// through a URL string (which would percent-encode the slashes and fail to parse,
// leaving the target permanently down). If the request never built, probes would
// stay 0 and the target would never come up.
func TestActiveHealthCheck_UnixSocketHostProbes(t *testing.T) {
	t.Parallel()
	sock := &healthFake{healthPath: "/hz"}
	targets := []*Target{{Host: "/var/run/app.sock:80", Transport: sock}}

	ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
	ahc.Path = "/hz"
	ahc.Interval = 5 * time.Millisecond
	ahc.HealthyThld = 1
	ahc.StartUnhealthy = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ahc.Start(ctx)
	defer ahc.Close()

	require.Eventually(t, func() bool { return sock.probes.Load() > 0 }, 2*time.Second, 5*time.Millisecond,
		"the unix-socket-host probe reaches the transport (the request built without a parse failure)")
	require.Eventually(t, func() bool { return ahc.up[0].Load() }, 2*time.Second, 5*time.Millisecond,
		"a healthy unix-socket target is admitted, not held permanently down")
}

func TestActiveHealthCheck_NonGateInnerNoPanic(t *testing.T) {
	t.Parallel()
	backend := &healthFake{healthPath: "/hz"}
	targets := []*Target{{Host: "t", Transport: backend}}
	// a custom RoundTripper that does NOT implement activeHealthGate
	custom := funcTransport(func(r *http.Request) (*http.Response, error) {
		r.URL.Host = targets[0].Host
		return targets[0].Transport.RoundTrip(r)
	})

	ahc := NewActiveHealthCheck(targets, custom)
	ahc.Path = "/hz"
	ahc.Interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	assert.NotPanics(t, func() {
		ahc.Start(ctx)
		resp, err := ahc.RoundTrip(httptest.NewRequest("GET", "/", nil))
		require.NoError(t, err)
		resp.Body.Close()
	})
	defer ahc.Close()
	require.Eventually(t, func() bool { return backend.probes.Load() > 0 }, 2*time.Second, 5*time.Millisecond,
		"probing runs even though the inner RoundTripper cannot be gated")
}

func TestActiveHealthCheck_CloseBeforeStart(t *testing.T) {
	t.Parallel()
	targets := []*Target{{Host: "t", Transport: freshBody()}}
	ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
	ahc.Interval = time.Hour

	require.NoError(t, ahc.Close()) // Close before any Start
	assert.NotPanics(t, func() {
		resp, err := ahc.RoundTrip(httptest.NewRequest("GET", "/", nil)) // a late first request
		require.NoError(t, err)
		resp.Body.Close()
	})

	ahc.mu.Lock()
	spawned := ahc.cancel != nil
	ahc.mu.Unlock()
	assert.False(t, spawned, "Close before Start spawns no prober, and a late RoundTrip never resurrects it")
}

func TestActiveHealthCheck_CloseDrainsNoLeak(t *testing.T) {
	// not parallel: NumGoroutine is process-global.
	base := runtime.NumGoroutine()
	const n = 5
	targets := make([]*Target, n)
	for i := range targets {
		targets[i] = &Target{Host: "t", Transport: freshBody()}
	}
	ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
	ahc.Interval = time.Hour // park on the ticker so Close drains promptly

	ahc.Start(context.Background())
	require.Eventually(t, func() bool { return runtime.NumGoroutine() >= base+n }, 2*time.Second, 10*time.Millisecond,
		"one probe goroutine per target")
	require.NoError(t, ahc.Close())
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= base+1 }, 2*time.Second, 10*time.Millisecond,
		"Close drains every probe goroutine back to baseline")
}

// StartCloseRace is the deterministic leak test for the lifecycle: a Close that
// races a first-RoundTrip start must never leak a prober. The race detector is BLIND
// to the leak (the fixed lifecycle is mutex-guarded, not racy), so the guard is a
// poll-to-baseline goroutine count, which fails on a timeout if any prober escaped.
func TestActiveHealthCheck_StartCloseRace(t *testing.T) {
	// not parallel: NumGoroutine is process-global.
	base := runtime.NumGoroutine()
	for range 50 {
		targets := []*Target{
			{Host: "a", Transport: freshBody()}, {Host: "b", Transport: freshBody()}, {Host: "c", Transport: freshBody()},
		}
		ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
		ahc.Interval = time.Hour
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); ahc.Start(context.Background()) }()
		go func() { defer wg.Done(); _ = ahc.Close() }()
		wg.Wait()
		// No post-loop Close() backstop on purpose: it would cancel any prober the
		// racing Close missed and mask the very leak this test measures — the poll
		// below must observe the race's OWN cleanup return to baseline.
	}
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= base+2 }, 3*time.Second, 10*time.Millisecond,
		"no probe goroutine leaked across 50 Start/Close races")
}

// LazyStartRegistersShutdown covers the lazy path: a first request served under a
// parapet.Server arms the prober and registers Close on graceful shutdown, so a
// SIGTERM stops probing without an explicit Close.
func TestActiveHealthCheck_LazyStartRegistersShutdown(t *testing.T) {
	// not parallel: drives a server Shutdown.
	backend := &healthFake{healthPath: "/hz"}
	targets := []*Target{{Host: "b", Transport: backend}}
	ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
	ahc.Path = "/hz"
	ahc.Interval = time.Hour

	srv := &parapet.Server{WaitBeforeShutdown: time.Millisecond}
	ctx := context.WithValue(context.Background(), parapet.ServerContextKey, srv)
	resp, err := ahc.RoundTrip(httptest.NewRequest("GET", "/", nil).WithContext(ctx))
	require.NoError(t, err)
	resp.Body.Close()

	require.Eventually(t, func() bool {
		ahc.mu.Lock()
		defer ahc.mu.Unlock()
		return ahc.cancel != nil
	}, time.Second, 5*time.Millisecond, "the first request lazily armed the prober")

	require.NoError(t, srv.Shutdown())
	require.Eventually(t, func() bool {
		ahc.mu.Lock()
		defer ahc.mu.Unlock()
		return ahc.closed
	}, 2*time.Second, 10*time.Millisecond, "graceful shutdown closed the prober via the registered hook")
}

// CloseBeforeStart with StartUnhealthy installs an all-DOWN gate that no prober will
// ever lift. For a shedding balancer (CircuitBreaking) that would 503 the pool
// forever, so start() must force the gate fail-open when it observes closed.
func TestActiveHealthCheck_CloseBeforeStartCircuitBreakerFailsOpen(t *testing.T) {
	t.Parallel()
	targets := []*Target{{Host: "t0", Transport: freshBody()}, {Host: "t1", Transport: freshBody()}}
	ahc := NewActiveHealthCheck(targets, NewCircuitBreakingLoadBalancer(targets))
	ahc.StartUnhealthy = true
	ahc.Interval = time.Hour

	require.NoError(t, ahc.Close()) // close before any start

	resp, err := ahc.RoundTrip(httptest.NewRequest("GET", "/", nil)) // a late request
	require.NoError(t, err, "a closed wrapper must fail the gate open, not dark-pool a shedding balancer")
	require.NotNil(t, resp)
	resp.Body.Close()
}

// schemeRecorder captures the URL scheme each probe request carries.
type schemeRecorder struct {
	scheme atomic.Pointer[string]
	probes atomic.Int64
}

func (s *schemeRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	sc := r.URL.Scheme
	s.scheme.Store(&sc)
	s.probes.Add(1)
	return hcResp(200), nil
}

// ProbeCarriesScheme guards that the probe carries the configured Scheme (not a
// hardcoded http), so a target on the dynamic multi-scheme Transport is probed over
// the protocol its data path uses, not silently mis-routed and driven down.
func TestActiveHealthCheck_ProbeCarriesScheme(t *testing.T) {
	t.Parallel()
	rec := &schemeRecorder{}
	targets := []*Target{{Host: "/var/run/app.sock:80", Transport: rec}}
	ahc := NewActiveHealthCheck(targets, NewRoundRobinLoadBalancer(targets))
	ahc.Scheme = "unix"
	ahc.Interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ahc.Start(ctx)
	defer ahc.Close()

	require.Eventually(t, func() bool { return rec.probes.Load() > 0 }, 2*time.Second, 5*time.Millisecond)
	if p := rec.scheme.Load(); assert.NotNil(t, p) {
		assert.Equal(t, "unix", *p, "the probe uses the configured Scheme for the dynamic Transport's dispatch")
	}
}
