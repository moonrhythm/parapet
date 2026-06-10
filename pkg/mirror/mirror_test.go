package mirror_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet"
	"github.com/moonrhythm/parapet/pkg/mirror"
)

func httputilReverseProxyTo(target string) http.Handler {
	u, _ := url.Parse(target)
	return httputil.NewSingleHostReverseProxy(u)
}

// recorder is a mirror-destination middleware that captures the request it receives
// and signals done. Behaviors: doPanic panics; gate (if set) blocks the handler until
// released OR the request context is cancelled (honoring the detached deadline).
type recorder struct {
	mu               sync.Mutex
	method           string
	host             string
	header           http.Header
	body             []byte
	contentLength    int64
	transferEncoding []string

	done        chan struct{}
	doPanic     bool
	gate        chan struct{}
	ctxCanceled atomic.Bool
	ctxHadValue atomic.Bool // did the mirror request inherit a client context value?
	seen        atomic.Int64
}

type testCtxKey struct{}

func newRecorder() *recorder { return &recorder{done: make(chan struct{}, 64)} }

func (rc *recorder) ServeHandler(http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rc.doPanic {
			panic("mirror boom")
		}
		if rc.gate != nil {
			select {
			case <-rc.gate:
			case <-r.Context().Done():
				rc.ctxCanceled.Store(true)
				return // context cancelled/timed out: free the worker
			}
		}
		rc.ctxHadValue.Store(r.Context().Value(testCtxKey{}) != nil)
		b, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.method = r.Method
		rc.host = r.Host
		rc.header = r.Header.Clone()
		rc.body = b
		rc.contentLength = r.ContentLength
		rc.transferEncoding = r.TransferEncoding
		rc.mu.Unlock()
		rc.seen.Add(1)
		rc.done <- struct{}{}
	})
}

func (rc *recorder) waitDone(t *testing.T) {
	t.Helper()
	select {
	case <-rc.done:
	case <-time.After(2 * time.Second):
		t.Fatal("mirror did not complete in time")
	}
}

func (rc *recorder) gotBody() []byte {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.body
}

// newMirror builds a Mirror with the recorder as its destination and small,
// deterministic pool sizes. Marking is disabled unless a test re-enables it.
func newMirror(rc *recorder) *mirror.Mirror {
	m := mirror.New()
	m.Use(rc)
	m.Workers = 4
	m.QueueSize = 16
	return m
}

func serve(m *mirror.Mirror, primary http.HandlerFunc, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	m.ServeHandler(primary).ServeHTTP(w, r)
	return w
}

func TestPrimaryUnaffectedNoBody(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	m := newMirror(rc)

	var primarySaw string
	w := serve(m, func(w http.ResponseWriter, r *http.Request) {
		primarySaw = r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("primary"))
	}, httptest.NewRequest("GET", "/path", nil))

	assert.Equal(t, "GET /path", primarySaw)
	assert.Equal(t, http.StatusTeapot, w.Code, "the primary response is untouched")
	assert.Equal(t, "primary", w.Body.String())
	rc.waitDone(t)
	assert.Equal(t, "GET", rc.method, "the mirror saw the request too")
}

// TestPrimaryBodyIdenticalUnderCap is the load-bearing test: independent readers, no
// shared cursor — both the primary and the mirror see the exact client bytes.
func TestPrimaryBodyIdenticalUnderCap(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	m := newMirror(rc)
	want := bytes.Repeat([]byte("abcd"), 1024) // 4 KiB

	var primaryBody []byte
	serve(m, func(_ http.ResponseWriter, r *http.Request) {
		primaryBody, _ = io.ReadAll(r.Body)
	}, httptest.NewRequest("POST", "/", bytes.NewReader(want)))

	assert.Equal(t, want, primaryBody, "the primary reads the whole original body")
	rc.waitDone(t)
	assert.Equal(t, want, rc.gotBody(), "the mirror reads byte-identical bytes")
}

func TestOverCapSkipsMirrorPrimaryWhole(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	m := newMirror(rc)
	m.MaxBodyBytes = 1024
	want := bytes.Repeat([]byte("x"), 1025) // cap+1

	var primaryBody []byte
	serve(m, func(_ http.ResponseWriter, r *http.Request) {
		primaryBody, _ = io.ReadAll(r.Body)
	}, httptest.NewRequest("POST", "/", bytes.NewReader(want)))

	assert.Equal(t, want, primaryBody, "primary still reads the COMPLETE oversize body")
	_, _, dropOversize, _, _ := m.Stats()
	assert.EqualValues(t, 1, dropOversize, "over-cap is skipped, not mirrored")
	assert.EqualValues(t, 0, rc.seen.Load())
}

func TestBodyExactlyAtCapMirrored(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	m := newMirror(rc)
	m.MaxBodyBytes = 1024
	want := bytes.Repeat([]byte("y"), 1024) // exactly cap

	serve(m, func(_ http.ResponseWriter, r *http.Request) { _, _ = io.ReadAll(r.Body) },
		httptest.NewRequest("POST", "/", bytes.NewReader(want)))

	rc.waitDone(t)
	assert.Equal(t, want, rc.gotBody(), "a body exactly at the cap is mirrored in full")
}

func TestReadErrorDeclines(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	m := newMirror(rc)

	boom := io.ErrUnexpectedEOF
	prefix := []byte("partial-bytes-consumed-during-capture")
	r := httptest.NewRequest("POST", "/", &partialErrReader{data: prefix, err: boom})
	var primaryBytes []byte
	var primaryErr error
	serve(m, func(_ http.ResponseWriter, r *http.Request) {
		primaryBytes, primaryErr = io.ReadAll(r.Body)
	}, r)

	// restoreBody must splice the bytes capture consumed ahead of the remainder so the
	// primary sees the exact same prefix-then-error it would without the middleware.
	assert.Equal(t, prefix, primaryBytes, "the primary reads the spliced-back consumed bytes")
	assert.ErrorIs(t, primaryErr, boom, "then observes the same read error, unswallowed")
	_, _, dropOversize, _, _ := m.Stats()
	assert.EqualValues(t, 1, dropOversize, "a read error declines the mirror")
}

func TestMirrorPanicIsolated(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	rc.doPanic = true
	m := newMirror(rc)

	const n = 5
	for range n {
		w := serve(m, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
			httptest.NewRequest("GET", "/", nil))
		assert.Equal(t, http.StatusOK, w.Code, "the primary is unaffected by a mirror panic")
	}

	require.Eventually(t, func() bool {
		_, _, _, _, panicked := m.Stats()
		return panicked == n
	}, 2*time.Second, 10*time.Millisecond, "every mirror panic is recovered, workers survive")
}

func TestSlowMirrorNeverBlocksPrimary(t *testing.T) {
	// not parallel: NumGoroutine is process-global. The fixed worker pool means the
	// goroutine count is bounded by Workers regardless of request count.
	baseGoroutines := runtime.NumGoroutine()
	rc := newRecorder()
	rc.gate = make(chan struct{}) // workers block until released at the end
	defer close(rc.gate)
	m := newMirror(rc)                  // Workers=4, QueueSize=16
	m.Timeout = 200 * time.Millisecond // gated workers free fast if a regression blocks dispatch

	const n = 200
	loopStart := time.Now()
	for range n {
		serve(m, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
			httptest.NewRequest("GET", "/", nil))
	}
	// Watchdog only: a per-iteration wall-clock bound gives a CI stall 200 chances to
	// fail a non-blocking path, so we bound the WHOLE loop instead. With all 4 workers
	// gated and the 16-slot queue full, a blocking dispatch would stall in 200ms
	// ctx-Timeout waves of 4 (the gated canary honors ctx.Done) — ~9s for 180 blocked
	// sends, comfortably past this bound — so 200 serves finishing inside it proves
	// the primary never waits; dropFull>0 below proves the drop path was exercised.
	assert.Less(t, time.Since(loopStart), 5*time.Second, "the primary never waits on the mirror")

	// Most are dropped (queue full); the pool never grows unbounded (Workers, not n).
	_, dropFull, _, _, _ := m.Stats()
	assert.Positive(t, dropFull, "surplus mirrors are dropped, not queued forever")
	assert.LessOrEqual(t, runtime.NumGoroutine(), baseGoroutines+m.Workers+8,
		"no goroutine-per-request leak (fixed worker pool)")
}

func TestDetachedContextNotCancelledByClient(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	rc.gate = make(chan struct{})
	m := newMirror(rc)

	ctx := context.WithValue(context.Background(), testCtxKey{}, "client-value")
	ctx, cancel := context.WithCancel(ctx)
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	serve(m, func(http.ResponseWriter, *http.Request) {}, r)

	cancel() // tear down the client request AFTER dispatch
	time.Sleep(20 * time.Millisecond)
	close(rc.gate) // let the mirror proceed
	rc.waitDone(t)
	assert.False(t, rc.ctxCanceled.Load(), "the mirror is NOT cancelled by the client (rooted at Background)")
	assert.False(t, rc.ctxHadValue.Load(),
		"the mirror does NOT inherit client ctx VALUES — proving Background(), which WithoutCancel would not")
}

func TestHungMirrorRespectsTimeout(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	rc.gate = make(chan struct{}) // never released: only the timeout frees the worker
	m := newMirror(rc)
	m.Timeout = 50 * time.Millisecond

	serve(m, func(http.ResponseWriter, *http.Request) {}, httptest.NewRequest("GET", "/", nil))

	// The bounded Eventually alone proves "the timeout frees the worker"; a separate
	// wall-clock cap raced worker scheduling against an inconsistent (smaller) budget.
	require.Eventually(t, func() bool { return rc.ctxCanceled.Load() }, 2*time.Second, 5*time.Millisecond,
		"the detached Timeout fires and frees the worker (no permanent worker pin)")
}

func TestHopByHopStrippedMarkedAndReframed(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	m := newMirror(rc)
	m.MarkHeader = "X-Mirror"
	m.MarkValue = "1"

	body := []byte("hello-canary")
	r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	r.Host = "app.example.com"
	r.Header.Set("Connection", "keep-alive")
	r.Header.Set("Keep-Alive", "timeout=5")
	r.Header.Set("Proxy-Authorization", "secret")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Expect", "100-continue")
	r.Header.Set("X-App", "keep-me")
	r.TransferEncoding = []string{"chunked"} // inbound chunked framing
	r.ContentLength = -1

	serve(m, func(_ http.ResponseWriter, r *http.Request) { _, _ = io.ReadAll(r.Body) }, r)
	rc.waitDone(t)

	rc.mu.Lock()
	defer rc.mu.Unlock()
	for _, h := range []string{"Connection", "Keep-Alive", "Proxy-Authorization", "Upgrade", "Expect"} {
		assert.Empty(t, rc.header.Get(h), "%s must be stripped from the mirror", h)
	}
	assert.Equal(t, "keep-me", rc.header.Get("X-App"), "ordinary headers are preserved")
	assert.Equal(t, "1", rc.header.Get("X-Mirror"), "the mirror is marked")
	assert.Equal(t, "app.example.com", rc.host, "Host (not hop-by-hop) reaches the canary")
	// The must-fix: chunked inbound framing must NOT override the fixed Content-Length.
	assert.Empty(t, rc.transferEncoding, "TransferEncoding cleared so it does not re-frame as chunked")
	assert.EqualValues(t, len(body), rc.contentLength, "the mirror carries the fixed Content-Length we set")
	assert.Equal(t, body, rc.body)
}

func TestDisableBody(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	m := newMirror(rc)
	m.DisableBody = true
	want := []byte("payload")

	var primaryBody []byte
	serve(m, func(_ http.ResponseWriter, r *http.Request) { primaryBody, _ = io.ReadAll(r.Body) },
		httptest.NewRequest("POST", "/", bytes.NewReader(want)))

	assert.Equal(t, want, primaryBody, "the primary body is intact")
	rc.waitDone(t)
	assert.Empty(t, rc.gotBody(), "the mirror gets no body when DisableBody")
}

func TestRequestURIClearedAndIntegration(t *testing.T) {
	t.Parallel()
	// Point the mirror at a real httptest server through a real reverse proxy so the
	// RequestURI-must-be-empty contract AND the chunked->Content-Length must-fix are
	// exercised end to end against a real transport (not just the clone).
	want := bytes.Repeat([]byte("z"), 3000)
	var (
		gotMirror   atomic.Bool
		gotBody     atomic.Bool
		gotLen      atomic.Int64
		gotChunked  atomic.Bool
		handlerDone atomic.Bool // set LAST: the completion signal the test gates on
	)
	canary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Mirror") == "1" {
			gotMirror.Store(true)
		}
		gotLen.Store(r.ContentLength)
		gotChunked.Store(len(r.TransferEncoding) > 0)
		b, _ := io.ReadAll(r.Body)
		gotBody.Store(bytes.Equal(b, want))
		w.WriteHeader(http.StatusOK)
		handlerDone.Store(true)
	}))
	defer canary.Close()

	m := mirror.New()
	m.Workers = 2
	m.UseFunc(func(http.Handler) http.Handler {
		return httputilReverseProxyTo(canary.URL)
	})

	r := httptest.NewRequest("POST", "/x", bytes.NewReader(want))
	r.TransferEncoding = []string{"chunked"} // inbound chunked framing
	r.ContentLength = -1
	serve(m, func(w http.ResponseWriter, r *http.Request) { _, _ = io.ReadAll(r.Body); w.WriteHeader(http.StatusOK) }, r)

	// Gate on handler COMPLETION, not its first side effect: gating on gotMirror alone
	// let the asserts below read gotBody/gotLen/gotChunked before the canary handler
	// had stored them.
	require.Eventually(t, handlerDone.Load, 2*time.Second, 10*time.Millisecond,
		"the mirrored request reached a real reverse-proxy destination (RequestURI cleared, no panic)")
	assert.True(t, gotMirror.Load(), "the mirrored request carried the mark to the canary")
	assert.True(t, gotBody.Load(), "the canary received the byte-identical body")
	assert.EqualValues(t, len(want), gotLen.Load(), "fixed Content-Length, not chunked-reframed")
	assert.False(t, gotChunked.Load(), "TransferEncoding=nil prevented chunked re-framing")
}

func TestSamplingDistribution(t *testing.T) {
	t.Parallel()
	t.Run("rate 0.25 within tolerance", func(t *testing.T) {
		rc := newRecorder()
		m := newMirror(rc)
		m.QueueSize = 1 << 16
		m.SampleRate = 0.25
		const n = 40000
		for range n {
			serve(m, func(http.ResponseWriter, *http.Request) {}, httptest.NewRequest("GET", "/", nil))
		}
		dispatched, _, _, _, _ := m.Stats()
		assert.InEpsilon(t, 0.25*n, float64(dispatched), 0.1, "≈25%% mirrored")
	})

	t.Run("Match gate excludes with zero dispatch", func(t *testing.T) {
		rc := newRecorder()
		m := newMirror(rc)
		m.Match = func(r *http.Request) bool { return r.Method == http.MethodPost }
		for range 20 {
			serve(m, func(http.ResponseWriter, *http.Request) {}, httptest.NewRequest("GET", "/", nil))
		}
		dispatched, _, _, _, _ := m.Stats()
		assert.Zero(t, dispatched, "non-matching requests are never mirrored")
	})
}

// TestObserveOutcomes drains to quiescence (every submitted request reached a
// terminal outcome) before asserting Stats agrees with the observed event counts.
func TestObserveOutcomes(t *testing.T) {
	t.Parallel()
	var byOutcome [5]atomic.Int64
	rc := newRecorder()
	m := newMirror(rc)
	m.Observe = func(info mirror.MirrorInfo) { byOutcome[info.Outcome].Add(1) }

	const n = 10
	for range n {
		serve(m, func(http.ResponseWriter, *http.Request) {}, httptest.NewRequest("GET", "/", nil))
	}

	// Quiescence barrier on the EMITTED-event side we assert against (not the separate
	// completed counter, which is incremented just before its emit — waiting on it
	// would race the OutcomeCompleted emit).
	require.Eventually(t, func() bool {
		return byOutcome[mirror.OutcomeDispatched].Load() == n && byOutcome[mirror.OutcomeCompleted].Load() == n
	}, 2*time.Second, 10*time.Millisecond)

	dispatched, dropFull, dropOversize, completed, panicked := m.Stats()
	assert.EqualValues(t, dispatched, byOutcome[mirror.OutcomeDispatched].Load())
	assert.EqualValues(t, completed, byOutcome[mirror.OutcomeCompleted].Load())
	assert.EqualValues(t, dropFull, byOutcome[mirror.OutcomeDroppedFull].Load())
	assert.EqualValues(t, dropOversize, byOutcome[mirror.OutcomeDroppedOversize].Load())
	assert.EqualValues(t, panicked, byOutcome[mirror.OutcomePanicked].Load())
	assert.EqualValues(t, n, completed, "all dispatched mirrors completed")
}

func TestComposeUnderBlock(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	m := newMirror(rc)
	// Mirror only requests whose path starts with /api (the kind of gating block does).
	m.Match = func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api") }

	serve(m, func(http.ResponseWriter, *http.Request) {}, httptest.NewRequest("GET", "/web", nil))
	serve(m, func(http.ResponseWriter, *http.Request) {}, httptest.NewRequest("GET", "/api/x", nil))

	rc.waitDone(t)
	dispatched, _, _, _, _ := m.Stats()
	assert.EqualValues(t, 1, dispatched, "only the matching request is teed")
}

// partialErrReader yields data, then fails with err — exercising the restore splice.
type partialErrReader struct {
	data []byte
	err  error
	off  int
}

func (e *partialErrReader) Read(p []byte) (int, error) {
	if e.off < len(e.data) {
		n := copy(p, e.data[e.off:])
		e.off += n
		return n, nil
	}
	return 0, e.err
}

// TestGetBodyFastPath exercises the captureBody GetBody branch (httptest.NewRequest
// does not set GetBody, so the rest of the suite only hits the io.CopyN path).
// http.NewRequest with a *bytes.Reader sets GetBody.
func TestGetBodyFastPath(t *testing.T) {
	t.Parallel()

	t.Run("under cap: primary untouched, mirror replays", func(t *testing.T) {
		rc := newRecorder()
		m := newMirror(rc)
		want := bytes.Repeat([]byte("g"), 2048)
		r, err := http.NewRequest("POST", "/", bytes.NewReader(want))
		require.NoError(t, err)
		require.NotNil(t, r.GetBody, "http.NewRequest sets GetBody for a bytes.Reader")

		var primaryBody []byte
		serve(m, func(_ http.ResponseWriter, r *http.Request) { primaryBody, _ = io.ReadAll(r.Body) }, r)
		assert.Equal(t, want, primaryBody, "the primary reads its untouched body")
		rc.waitDone(t)
		assert.Equal(t, want, rc.gotBody(), "the mirror replays the identical bytes via GetBody")
	})

	t.Run("over cap declines, primary whole", func(t *testing.T) {
		rc := newRecorder()
		m := newMirror(rc)
		m.MaxBodyBytes = 1024
		want := bytes.Repeat([]byte("h"), 2048) // > cap
		r, err := http.NewRequest("POST", "/", bytes.NewReader(want))
		require.NoError(t, err)

		var primaryBody []byte
		serve(m, func(_ http.ResponseWriter, r *http.Request) { primaryBody, _ = io.ReadAll(r.Body) }, r)
		assert.Equal(t, want, primaryBody, "the primary still reads the whole oversize body")
		_, _, dropOversize, _, _ := m.Stats()
		assert.EqualValues(t, 1, dropOversize, "the GetBody path declines over-cap too")
	})
}

func TestDisableMark(t *testing.T) {
	t.Parallel()
	rc := newRecorder()
	m := newMirror(rc)
	m.DisableMark = true // a fully transparent mirror

	serve(m, func(http.ResponseWriter, *http.Request) {}, httptest.NewRequest("GET", "/", nil))
	rc.waitDone(t)

	rc.mu.Lock()
	defer rc.mu.Unlock()
	assert.Empty(t, rc.header.Get("X-Mirror"), "DisableMark omits the mark header")
}

var _ parapet.Middleware = (*recorder)(nil)
