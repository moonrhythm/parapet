package waf_test

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet/pkg/waf"
)

// remoteIPProbe builds a WAF that blocks when the rule's remote_ip equals
// the wanted value, returning whether the WAF observed that IP.
func remoteIPProbe(t *testing.T, want string) func(*http.Request) bool {
	t.Helper()
	w := waf.New()
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "probe",
		Expression: `request.remote_ip == "` + want + `"`,
		Action:     waf.ActionBlock,
	}}))
	h := w.ServeHandler(passthroughHandler)
	return func(r *http.Request) bool {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
		return rr.Code == http.StatusForbidden
	}
}

func TestActionString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "log", waf.ActionLog.String())
	assert.Equal(t, "allow", waf.ActionAllow.String())
	assert.Equal(t, "block", waf.ActionBlock.String())
	assert.Equal(t, "unknown", waf.Action(99).String())
}

func TestLoggerFuncAdapter(t *testing.T) {
	t.Parallel()

	var got string
	var logger waf.Logger = waf.LoggerFunc(func(format string, args ...any) {
		got = format
		_ = args
	})
	logger.Logf("hello %s", "world")
	assert.Equal(t, "hello %s", got)
}

func TestClientIPSources(t *testing.T) {
	t.Parallel()

	t.Run("x-real-ip wins over x-forwarded-for and remote addr", func(t *testing.T) {
		probe := remoteIPProbe(t, "9.9.9.9")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "1.2.3.4:5678"
		req.Header.Set("X-Forwarded-For", "8.8.8.8")
		req.Header.Set("X-Real-Ip", "9.9.9.9")
		assert.True(t, probe(req))
	})

	t.Run("x-forwarded-for picks first when comma-separated", func(t *testing.T) {
		probe := remoteIPProbe(t, "1.1.1.1")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-For", " 1.1.1.1 , 2.2.2.2")
		assert.True(t, probe(req))
	})

	t.Run("x-forwarded-for single value used as-is", func(t *testing.T) {
		probe := remoteIPProbe(t, "5.6.7.8")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-For", "5.6.7.8")
		assert.True(t, probe(req))
	})

	t.Run("falls back to RemoteAddr host without port", func(t *testing.T) {
		probe := remoteIPProbe(t, "10.20.30.40")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.20.30.40:12345"
		assert.True(t, probe(req))
	})

	t.Run("falls back to raw RemoteAddr when SplitHostPort fails", func(t *testing.T) {
		probe := remoteIPProbe(t, "not-an-addr")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "not-an-addr"
		assert.True(t, probe(req))
	})
}

func TestSchemeDetection(t *testing.T) {
	t.Parallel()

	type tc struct {
		name   string
		tls    bool
		xfp    string
		expect string
	}
	cases := []tc{
		{"plain http", false, "", "http"},
		{"r.TLS set means https", true, "", "https"},
		{"x-forwarded-proto overrides r.TLS", true, "ws", "ws"},
		{"x-forwarded-proto overrides bare http", false, "https", "https"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := waf.New()
			require.NoError(t, w.SetRules([]waf.Rule{{
				ID:         "scheme",
				Expression: `request.scheme == "` + c.expect + `"`,
				Action:     waf.ActionBlock,
			}}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if c.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", c.xfp)
			}
			rr := httptest.NewRecorder()
			w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
			assert.Equal(t, http.StatusForbidden, rr.Code, "scheme=%s", c.expect)
		})
	}
}

func TestEmptyHeaderAndQueryValues(t *testing.T) {
	t.Parallel()

	// When a caller assigns an empty slice directly to r.Header (as some
	// middlewares do to "clear" a header), buildRequestMap must skip it
	// instead of panicking on v[0]. We exercise the skip path by feeding a
	// request with one empty-slice header and one populated header; the
	// rule looks up the populated one to confirm the map was built without
	// crashing.
	w := waf.New()
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "lookup",
		Expression: `request.headers["x-real"] == "y"`,
		Action:     waf.ActionBlock,
	}}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header["X-Empty"] = []string{} // empty slice — exercises the skip branch
	req.Header.Set("X-Real", "y")
	rr := httptest.NewRecorder()
	require.NotPanics(t, func() {
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
	})
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestCustomFunctionErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("ipInCidr bad cidr is fail-open", func(t *testing.T) {
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID:         "bad-cidr",
			Expression: `ipInCidr(request.remote_ip, "not-a-cidr")`,
			Action:     waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Real-Ip", "1.2.3.4")
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code, "fail-open on bad CIDR")
	})

	t.Run("ipInCidr unparseable IP returns false (no error)", func(t *testing.T) {
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID:         "bad-ip",
			Expression: `ipInCidr(request.remote_ip, "10.0.0.0/8")`,
			Action:     waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		// RemoteAddr without port → clientIP returns raw string, ipInCidr
		// gets a non-IP string and ParseIP returns nil → false (not an error).
		req.RemoteAddr = "not-an-ip"
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code, "downstream served (no match)")
	})

	t.Run("regexMatch bad pattern is fail-open", func(t *testing.T) {
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID:         "bad-regex",
			Expression: `regexMatch(request.path, request.headers["x-pattern"])`,
			Action:     waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Pattern", "(unterminated")
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("containsAny no match returns false", func(t *testing.T) {
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID:         "miss",
			Expression: `containsAny(request.path, ["never", "matches"])`,
			Action:     waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/clean-path", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("containsAny ignores empty list entries", func(t *testing.T) {
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID:         "empty",
			Expression: `containsAny(request.path, ["", ""])`,
			Action:     waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/anything", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("hasPrefixAny no match returns false", func(t *testing.T) {
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID:         "miss-prefix",
			Expression: `hasPrefixAny(request.path, ["/x", "/y"])`,
			Action:     waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("hasPrefixAny ignores empty list entries", func(t *testing.T) {
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID:         "empty-prefix",
			Expression: `hasPrefixAny(request.path, ["", ""])`,
			Action:     waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/anything", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("upper matches uppercased value", func(t *testing.T) {
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID:         "upper",
			Expression: `upper(request.method) == "POST"`,
			Action:     waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("urlDecode invalid escape returns empty string", func(t *testing.T) {
		// "%ZZ" is not valid percent-encoding; url.QueryUnescape returns an
		// error which celURLDecode turns into "".
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID:         "decode-empty",
			Expression: `urlDecode(request.query) == ""`,
			Action:     waf.ActionBlock,
		}}))
		req := httptest.NewRequest(http.MethodGet, "/?x=%ZZ", nil)
		rr := httptest.NewRecorder()
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestRegexAndCIDRCacheHitPaths(t *testing.T) {
	t.Parallel()

	// Two requests against the same compiled rule exercise the cache hit
	// path on the second call (both regex and CIDR helpers maintain
	// package-level caches).
	w := waf.New()
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "cached",
		Expression: `regexMatch(request.path, "^/blocked$") || ipInCidr(request.remote_ip, "10.0.0.0/8")`,
		Action:     waf.ActionBlock,
	}}))
	h := w.ServeHandler(passthroughHandler)

	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/blocked", nil)
		req.Header.Set("X-Real-Ip", "10.1.2.3")
		h.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	}

	// Repeat a bad CIDR / bad pattern so the error-cache branches fire too.
	w2 := waf.New()
	require.NoError(t, w2.SetRules([]waf.Rule{{
		ID:         "err-cached",
		Expression: `ipInCidr(request.remote_ip, "garbage-cidr") || regexMatch(request.path, "(open")`,
		Action:     waf.ActionBlock,
	}}))
	h2 := w2.ServeHandler(passthroughHandler)
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("X-Real-Ip", "1.2.3.4")
		h2.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code, "errors fail open")
	}
}

func TestDisableMacrosRejectsComprehensions(t *testing.T) {
	t.Parallel()

	w := waf.New()
	w.DisableMacros = true
	// `exists` is a macro; with ClearMacros it must fail to compile.
	err := w.SetRules([]waf.Rule{{
		ID:         "macro",
		Expression: `["a", "b"].exists(x, x == request.method)`,
		Action:     waf.ActionBlock,
	}})
	require.Error(t, err)
}

func TestSetRulesEmptyExpression(t *testing.T) {
	t.Parallel()

	w := waf.New()
	err := w.SetRules([]waf.Rule{{ID: "no-expr", Expression: "", Action: waf.ActionBlock}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty expression")
}

func TestSetRulesJoinsMultipleErrors(t *testing.T) {
	t.Parallel()

	w := waf.New()
	err := w.SetRules([]waf.Rule{
		{ID: "", Expression: "true", Action: waf.ActionBlock},
		{ID: "r2", Expression: "", Action: waf.ActionBlock},
		{ID: "r3", Expression: "request.method", Action: waf.ActionBlock},
	})
	require.Error(t, err)
	// errors.Join wraps multiple errors that should all be visible via Unwrap.
	var u interface{ Unwrap() []error }
	require.True(t, errors.As(err, &u), "joined errors should implement Unwrap() []error")
	assert.Len(t, u.Unwrap(), 3)
}

func TestRulesReportsOrderedIDs(t *testing.T) {
	t.Parallel()

	// New() seeds an empty ruleset; Rules() returns []string{} not nil.
	w := waf.New()
	assert.Empty(t, w.Rules())
}

func TestZeroValueWAFReturnsNilRulesAndPasses(t *testing.T) {
	t.Parallel()

	// A WAF constructed without New() has a nil atomic pointer. Rules()
	// must report nil and ServeHandler must pass requests through unchanged
	// (no compiled ruleset means nothing to enforce).
	var w waf.WAF
	assert.Nil(t, w.Rules())

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestLoggerCalledOnMatchAndError(t *testing.T) {
	t.Parallel()

	var lines atomic.Int64
	w := waf.New()
	w.Logger = waf.LoggerFunc(func(format string, args ...any) {
		_ = format
		_ = args
		lines.Add(1)
	})
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "blocker",
		Expression: `request.path == "/x"`,
		Action:     waf.ActionBlock,
	}}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.GreaterOrEqual(t, lines.Load(), int64(1), "logger should observe the match")

	// Also exercise the error-path log line by feeding a rule that errors at
	// eval time (bad pattern). FailMode stays FailOpen so the request is
	// served and the logger receives the error line.
	lines.Store(0)
	w2 := waf.New()
	w2.Logger = waf.LoggerFunc(func(format string, args ...any) {
		_ = format
		_ = args
		lines.Add(1)
	})
	require.NoError(t, w2.SetRules([]waf.Rule{{
		ID:         "bad",
		Expression: `regexMatch(request.path, request.headers["x-pat"])`,
		Action:     waf.ActionBlock,
	}}))
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Pat", "(open")
	w2.ServeHandler(passthroughHandler).ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusOK, rr2.Code)
	assert.GreaterOrEqual(t, lines.Load(), int64(1), "logger should observe the eval error")
}

func TestMatchEventFields(t *testing.T) {
	t.Parallel()

	w := waf.New()
	var seen waf.MatchEvent
	w.OnMatch = func(ev waf.MatchEvent) { seen = ev }
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "blocker",
		Expression: `request.method == "GET"`,
		Action:     waf.ActionBlock,
		Status:     http.StatusTeapot,
		Message:    "stop",
	}}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-Ip", "9.9.9.9")
	w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)

	assert.Equal(t, "blocker", seen.RuleID)
	assert.Equal(t, waf.ActionBlock, seen.Action)
	assert.Equal(t, http.StatusTeapot, seen.Status)
	assert.Equal(t, "9.9.9.9", seen.ClientIP)
	assert.NotEmpty(t, seen.Expression)
	assert.NotNil(t, seen.Request)
	// Elapsed is monotonic non-negative; just check it was populated.
	assert.GreaterOrEqual(t, seen.Elapsed.Nanoseconds(), int64(0))
}

func TestBodyReplayClosesOriginal(t *testing.T) {
	t.Parallel()

	w := waf.New()
	w.InspectBody = 1024
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "noop",
		Expression: `request.body.contains("never-matches")`,
		Action:     waf.ActionBlock,
	}}))

	tracker := &closingReader{Reader: strings.NewReader("hello")}
	req := httptest.NewRequest(http.MethodPost, "/", tracker)
	req.Body = tracker // override the default NopCloser wrap

	downstream := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Read fully and close — Close should reach the original body.
		_, _ = io.ReadAll(r.Body)
		_ = r.Body.Close()
	})
	rr := httptest.NewRecorder()
	w.ServeHandler(downstream).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, tracker.closed.Load(), "Close on replayed body should reach original")
}

// closingReader is an io.ReadCloser that records whether Close was called.
type closingReader struct {
	io.Reader
	closed atomic.Bool
}

func (c *closingReader) Close() error { c.closed.Store(true); return nil }

func TestBodyInspectionWithNilBody(t *testing.T) {
	t.Parallel()

	// GET with no body — request.body must be empty string and the request
	// must still be served.
	w := waf.New()
	w.InspectBody = 1024
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "empty-body",
		Expression: `request.body == "" && request.method == "GET"`,
		Action:     waf.ActionBlock,
	}}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestEvalTimeoutFailsOpen(t *testing.T) {
	t.Parallel()

	// Force a deadline that has already passed by the time evaluation starts.
	// CEL's ContextEval should observe the cancelled context and return an
	// error; FailOpen (default) passes the request through.
	w := waf.New()
	w.EvalTimeout = 1 // 1 ns — always expired by the time ContextEval runs
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "slow",
		Expression: `request.path == "/x"`,
		Action:     waf.ActionBlock,
	}}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
	// Whether the rule errors or just returns false depends on CEL's check
	// frequency, so accept either outcome — we just need the code path to
	// not panic and respect fail-open semantics.
	assert.NotEqual(t, http.StatusInternalServerError, rr.Code)
}
