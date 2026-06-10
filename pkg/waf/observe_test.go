package waf_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet/pkg/waf"
)

// recordObserve installs a capturing Observe hook and returns the slice it fills.
func recordObserve(w *waf.WAF) *[]waf.EvalEvent {
	var events []waf.EvalEvent
	w.Observe = func(e waf.EvalEvent) { events = append(events, e) }
	return &events
}

func driveWAF(w *waf.WAF, method, target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
	return rr
}

func TestObserve_Pass_NoMatch(t *testing.T) {
	t.Parallel()
	// The silent-majority path OnMatch cannot see: a rule that doesn't match,
	// request falls through to downstream.
	w := newWAF(t, []waf.Rule{{ID: "r", Expression: `request.method == "POST"`, Action: waf.ActionBlock}})
	ev := recordObserve(w)
	rr := driveWAF(w, http.MethodGet, "/")

	require.Len(t, *ev, 1, "exactly one Observe per request, even with no match")
	assert.Equal(t, waf.OutcomePass, (*ev)[0].Outcome)
	assert.GreaterOrEqual(t, (*ev)[0].Duration, time.Duration(0))
	assert.NotNil(t, (*ev)[0].Request)
	assert.Equal(t, "downstream-ok", rr.Body.String())
}

func TestObserve_Block(t *testing.T) {
	t.Parallel()
	w := newWAF(t, []waf.Rule{{ID: "r", Expression: `request.path.startsWith("/admin")`, Action: waf.ActionBlock, Status: http.StatusForbidden}})
	ev := recordObserve(w)
	var matches int
	w.OnMatch = func(waf.MatchEvent) { matches++ }
	rr := driveWAF(w, http.MethodGet, "/admin/x")

	require.Len(t, *ev, 1)
	assert.Equal(t, waf.OutcomeBlock, (*ev)[0].Outcome)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Equal(t, 1, matches, "OnMatch and Observe are independent hooks, each fires once")
}

func TestObserve_Allow(t *testing.T) {
	t.Parallel()
	// An allow rule (lower Priority => runs first) short-circuits ahead of a
	// matching block rule, which must NOT be recorded.
	w := newWAF(t, []waf.Rule{
		{ID: "allow", Expression: `true`, Action: waf.ActionAllow, Priority: 0},
		{ID: "block", Expression: `true`, Action: waf.ActionBlock, Priority: 10},
	})
	ev := recordObserve(w)
	rr := driveWAF(w, http.MethodGet, "/")

	require.Len(t, *ev, 1, "allow short-circuits; only one event, not one per rule")
	assert.Equal(t, waf.OutcomeAllow, (*ev)[0].Outcome)
	assert.Equal(t, "downstream-ok", rr.Body.String())
}

func TestObserve_LogThenPass(t *testing.T) {
	t.Parallel()
	// A log-only match does not terminate; it must fold into a SINGLE pass event.
	w := newWAF(t, []waf.Rule{{ID: "log", Expression: `true`, Action: waf.ActionLog}})
	ev := recordObserve(w)
	var matches int
	w.OnMatch = func(waf.MatchEvent) { matches++ }
	rr := driveWAF(w, http.MethodGet, "/")

	require.Len(t, *ev, 1, "a non-terminating log match emits no extra Observe event")
	assert.Equal(t, waf.OutcomePass, (*ev)[0].Outcome)
	assert.Equal(t, 1, matches)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestObserve_FiresOnce_MultiMatch(t *testing.T) {
	t.Parallel()
	// Two log matches then a block match: OnMatch fires 3x but Observe once.
	w := newWAF(t, []waf.Rule{
		{ID: "log1", Expression: `true`, Action: waf.ActionLog, Priority: 0},
		{ID: "log2", Expression: `true`, Action: waf.ActionLog, Priority: 1},
		{ID: "block", Expression: `true`, Action: waf.ActionBlock, Priority: 2},
	})
	ev := recordObserve(w)
	var matches int
	w.OnMatch = func(waf.MatchEvent) { matches++ }
	driveWAF(w, http.MethodGet, "/")

	assert.Equal(t, 3, matches, "OnMatch fires per matched rule")
	require.Len(t, *ev, 1, "Observe fires exactly once regardless of match count")
	assert.Equal(t, waf.OutcomeBlock, (*ev)[0].Outcome)
}

func TestObserve_ErrorFailClosed(t *testing.T) {
	t.Parallel()
	// CostLimit=1 + a request.*-touching rule forces a runtime eval error; under
	// FailClosed it is the only path to OutcomeError (a 500).
	w := waf.New()
	w.CostLimit = 1
	w.FailMode = waf.FailClosed
	require.NoError(t, w.SetRules([]waf.Rule{{ID: "expensive", Expression: `request.path == request.method`, Action: waf.ActionBlock}}))
	ev := recordObserve(w)
	rr := driveWAF(w, http.MethodGet, "/some/path")

	require.Len(t, *ev, 1)
	assert.Equal(t, waf.OutcomeError, (*ev)[0].Outcome)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestObserve_ErrorFailOpen(t *testing.T) {
	t.Parallel()
	// The SAME error under the default FailOpen is swallowed and must NOT become
	// OutcomeError — it folds into pass.
	w := waf.New()
	w.CostLimit = 1 // FailMode defaults to FailOpen
	require.NoError(t, w.SetRules([]waf.Rule{{ID: "expensive", Expression: `request.path == request.method`, Action: waf.ActionBlock}}))
	ev := recordObserve(w)
	rr := driveWAF(w, http.MethodGet, "/some/path")

	require.Len(t, *ev, 1)
	assert.Equal(t, waf.OutcomePass, (*ev)[0].Outcome, "a FailOpen-swallowed error is not OutcomeError")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestObserve_NoRulesFastPath(t *testing.T) {
	t.Parallel()
	// No rules loaded: the WAF does no evaluation, so Observe must not fire.
	w := waf.New()
	ev := recordObserve(w)
	rr := driveWAF(w, http.MethodGet, "/")

	assert.Empty(t, *ev, "the no-rules fast path performs no evaluation and emits no event")
	assert.Equal(t, "downstream-ok", rr.Body.String())
}

func TestObserve_NilHook(t *testing.T) {
	t.Parallel()
	// A nil Observe is a no-op, not a panic.
	w := newWAF(t, []waf.Rule{{ID: "r", Expression: `true`, Action: waf.ActionBlock, Status: http.StatusForbidden}})
	// w.Observe left nil
	assert.NotPanics(t, func() {
		rr := driveWAF(w, http.MethodGet, "/")
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestObserve_DurationExcludesDownstream(t *testing.T) {
	t.Parallel()
	// Duration is measured at the decision point, BEFORE h.ServeHTTP, so a slow
	// downstream handler must not leak into the WAF eval duration.
	w := newWAF(t, []waf.Rule{{ID: "r", Expression: `request.method == "POST"`, Action: waf.ActionBlock}})
	ev := recordObserve(w)
	slow := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		time.Sleep(30 * time.Millisecond)
		rw.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	w.ServeHandler(slow).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Len(t, *ev, 1)
	assert.Equal(t, waf.OutcomePass, (*ev)[0].Outcome)
	assert.Less(t, (*ev)[0].Duration, 30*time.Millisecond,
		"eval Duration excludes downstream latency (the 30ms sleep dwarfs eval, so any leak is caught)")
}

func TestOutcomeString(t *testing.T) {
	t.Parallel()
	for o, s := range map[waf.Outcome]string{
		waf.OutcomePass:  "pass",
		waf.OutcomeAllow: "allow",
		waf.OutcomeBlock: "block",
		waf.OutcomeError: "error",
	} {
		assert.Equal(t, s, o.String())
	}
	assert.Equal(t, "unknown", waf.Outcome(200).String(), "an out-of-range value never produces an unbounded label")
}
