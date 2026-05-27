package waf_test

import (
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

	"github.com/moonrhythm/parapet/pkg/waf"
)

// passthroughHandler is the default downstream — returns 200 OK with a marker
// body so tests can distinguish "WAF blocked" from "downstream served".
var passthroughHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("downstream-ok"))
})

func newWAF(t *testing.T, rules []waf.Rule) *waf.WAF {
	t.Helper()
	w := waf.New()
	require.NoError(t, w.SetRules(rules))
	return w
}

func TestSetRules_Validation(t *testing.T) {
	t.Parallel()

	t.Run("empty ID rejected", func(t *testing.T) {
		w := waf.New()
		err := w.SetRules([]waf.Rule{{Expression: "true", Action: waf.ActionBlock}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing ID")
	})

	t.Run("duplicate ID rejected", func(t *testing.T) {
		w := waf.New()
		err := w.SetRules([]waf.Rule{
			{ID: "r1", Expression: "true", Action: waf.ActionBlock},
			{ID: "r1", Expression: "false", Action: waf.ActionBlock},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})

	t.Run("non-bool expression rejected", func(t *testing.T) {
		w := waf.New()
		err := w.SetRules([]waf.Rule{{ID: "r1", Expression: `request.method`, Action: waf.ActionBlock}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must return bool")
	})

	t.Run("syntax error rejected", func(t *testing.T) {
		w := waf.New()
		err := w.SetRules([]waf.Rule{{ID: "r1", Expression: `request..method == "GET"`, Action: waf.ActionBlock}})
		require.Error(t, err)
	})

	t.Run("unknown variable rejected", func(t *testing.T) {
		w := waf.New()
		err := w.SetRules([]waf.Rule{{ID: "r1", Expression: `mystery == "x"`, Action: waf.ActionBlock}})
		require.Error(t, err)
	})

	t.Run("bad rules do not replace existing ruleset", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{{ID: "r1", Expression: `request.method == "GET"`, Action: waf.ActionBlock}})
		require.Equal(t, []string{"r1"}, w.Rules())

		// Now try to install a broken rule.
		err := w.SetRules([]waf.Rule{{ID: "broken", Expression: "this is not cel", Action: waf.ActionBlock}})
		require.Error(t, err)

		// Old rule must still be active.
		assert.Equal(t, []string{"r1"}, w.Rules())
	})
}

func TestActions(t *testing.T) {
	t.Parallel()

	t.Run("block returns configured status and body", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{{
			ID:         "block-admin",
			Expression: `request.path.startsWith("/admin")`,
			Action:     waf.ActionBlock,
			Status:     http.StatusForbidden,
			Message:    "nope",
		}})

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.Contains(t, rr.Body.String(), "nope")
	})

	t.Run("non-matching request passes through", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{{
			ID:         "block-admin",
			Expression: `request.path.startsWith("/admin")`,
			Action:     waf.ActionBlock,
		}})

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/public", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "downstream-ok", rr.Body.String())
	})

	t.Run("allow short-circuits later block rules", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{
			// Allow rule fires first because of lower priority.
			{ID: "allow-internal", Expression: `request.headers["x-internal"] == "yes"`, Action: waf.ActionAllow, Priority: 0},
			{ID: "block-admin", Expression: `request.path.startsWith("/admin")`, Action: waf.ActionBlock, Priority: 10},
		})

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("X-Internal", "yes")
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("log action does not block the request", func(t *testing.T) {
		var loggedRules []string
		var mu sync.Mutex

		w := waf.New()
		w.OnMatch = func(ev waf.MatchEvent) {
			mu.Lock()
			loggedRules = append(loggedRules, ev.RuleID)
			mu.Unlock()
		}
		require.NoError(t, w.SetRules([]waf.Rule{
			{ID: "log-only", Expression: `request.method == "GET"`, Action: waf.ActionLog, Priority: 0},
			{ID: "block-admin", Expression: `request.path.startsWith("/admin")`, Action: waf.ActionBlock, Priority: 10},
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/public", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, []string{"log-only"}, loggedRules)
	})

	t.Run("default block status is 403", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{{
			ID: "r1", Expression: "true", Action: waf.ActionBlock,
		}})
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestExpressionVariables(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		expr   string
		setup  func(*http.Request)
		expect int // 200 = passthrough, 403 = blocked
	}{
		{
			name:   "method match",
			expr:   `request.method == "DELETE"`,
			setup:  func(r *http.Request) { r.Method = http.MethodDelete },
			expect: http.StatusForbidden,
		},
		{
			name: "header lookup is lowercase",
			expr: `request.headers["x-bad"] == "1"`,
			setup: func(r *http.Request) {
				r.Header.Set("X-Bad", "1")
			},
			expect: http.StatusForbidden,
		},
		{
			name: "user_agent shortcut",
			expr: `request.user_agent.contains("sqlmap")`,
			setup: func(r *http.Request) {
				r.Header.Set("User-Agent", "sqlmap/1.0")
			},
			expect: http.StatusForbidden,
		},
		{
			name: "query args parsed",
			expr: `request.args["id"] == "../../etc/passwd"`,
			setup: func(r *http.Request) {
				q := r.URL.Query()
				q.Set("id", "../../etc/passwd")
				r.URL.RawQuery = q.Encode()
			},
			expect: http.StatusForbidden,
		},
		{
			name: "cookie parsed",
			expr: `request.cookies["session"] == "stolen"`,
			setup: func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "session", Value: "stolen"})
			},
			expect: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWAF(t, []waf.Rule{{ID: "r", Expression: tc.expr, Action: waf.ActionBlock}})
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tc.setup != nil {
				tc.setup(req)
			}
			rr := httptest.NewRecorder()
			w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
			assert.Equal(t, tc.expect, rr.Code, "expr=%q", tc.expr)
		})
	}
}

func TestCustomFunctions(t *testing.T) {
	t.Parallel()

	t.Run("ipInCidr matches", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{{
			ID:         "block-internal",
			Expression: `ipInCidr(request.remote_ip, "10.0.0.0/8")`,
			Action:     waf.ActionBlock,
		}})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Real-Ip", "10.5.6.7")
		rr := httptest.NewRecorder()
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("ipInCidr non-match passes", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{{
			ID:         "block-internal",
			Expression: `ipInCidr(request.remote_ip, "10.0.0.0/8")`,
			Action:     waf.ActionBlock,
		}})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Real-Ip", "8.8.8.8")
		rr := httptest.NewRecorder()
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("regexMatch sql injection signature", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{{
			ID: "block-sqli",
			// Use urlDecode so URL-encoded spaces (+ or %20) are normalised
			// before the regex sees them — a common WAF normalisation step.
			Expression: `regexMatch(lower(urlDecode(request.query)), "(union\\s+select|or\\s+1=1)")`,
			Action:     waf.ActionBlock,
		}})
		req := httptest.NewRequest(http.MethodGet, "/?q=1+UNION+SELECT+pass", nil)
		rr := httptest.NewRecorder()
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("containsAny short-circuits", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{{
			ID:         "scanners",
			Expression: `containsAny(lower(request.user_agent), ["sqlmap", "nikto", "acunetix"])`,
			Action:     waf.ActionBlock,
		}})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 NIKTO scanner")
		rr := httptest.NewRecorder()
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("hasPrefixAny matches", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{{
			ID:         "block-paths",
			Expression: `hasPrefixAny(request.path, ["/admin", "/internal", "/.git"])`,
			Action:     waf.ActionBlock,
		}})
		req := httptest.NewRequest(http.MethodGet, "/.git/config", nil)
		rr := httptest.NewRecorder()
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("urlDecode normalises", func(t *testing.T) {
		w := newWAF(t, []waf.Rule{{
			ID:         "decode-and-match",
			Expression: `urlDecode(request.query).contains("../")`,
			Action:     waf.ActionBlock,
		}})
		req := httptest.NewRequest(http.MethodGet, "/?file=%2E%2E%2Fetc%2Fpasswd", nil)
		rr := httptest.NewRecorder()
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestHotReload(t *testing.T) {
	t.Parallel()

	w := newWAF(t, []waf.Rule{{
		ID: "v1", Expression: `request.path == "/v1"`, Action: waf.ActionBlock,
	}})

	var concurrent atomic.Int64
	var maxConcurrent atomic.Int64
	updateMax := func(n int64) {
		for {
			cur := maxConcurrent.Load()
			if n <= cur || maxConcurrent.CompareAndSwap(cur, n) {
				return
			}
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := w.ServeHandler(passthroughHandler)
			for {
				select {
				case <-stop:
					return
				default:
				}
				updateMax(concurrent.Add(1))
				rr := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/v1", nil)
				h.ServeHTTP(rr, req)
				concurrent.Add(-1)
				// Status may be 200 or 403 depending on which ruleset is
				// active right now — both are valid; we just want zero
				// crashes / data races (tests run with -race).
				_ = rr.Code
			}
		}()
	}

	// Hot-swap the ruleset many times while requests are in flight.
	for i := 0; i < 50; i++ {
		err := w.SetRules([]waf.Rule{{
			ID: "v" + itoa(i+2), Expression: `request.path == "/" + "v1"`, Action: waf.ActionBlock,
		}})
		require.NoError(t, err)
		time.Sleep(time.Millisecond)
	}
	close(stop)
	wg.Wait()

	assert.Greater(t, maxConcurrent.Load(), int64(1), "expected concurrent traffic during reload")
}

func TestCostLimit(t *testing.T) {
	t.Parallel()

	// CostLimit=1 means the very first runtime op aborts. We pick a rule
	// that touches request.* (so it isn't constant-folded by OptOptimize)
	// to exercise the runtime cost-tracking path rather than compile-time
	// folding.
	w := waf.New()
	w.CostLimit = 1
	w.FailMode = waf.FailClosed
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "expensive",
		Expression: `request.path == request.method`,
		Action:     waf.ActionBlock,
	}}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/some/path", nil)
	w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
	// FailClosed converts the cost-exceeded error to 500.
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestFailOpenIsDefault(t *testing.T) {
	t.Parallel()

	w := waf.New()
	// regexMatch with a runtime-supplied (non-constant) pattern bypasses
	// the OptOptimize compile-time fold, so a bad pattern errors at eval.
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "bad-runtime",
		Expression: `regexMatch(request.headers["x-foo"], request.headers["x-pattern"])`,
		Action:     waf.ActionBlock,
	}}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Foo", "bar")
	req.Header.Set("X-Pattern", "(?P<a>x") // unterminated group
	w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code, "fail-open should pass on rule errors")
}

func TestBodyInspection(t *testing.T) {
	t.Parallel()

	w := waf.New()
	w.InspectBody = 1024
	require.NoError(t, w.SetRules([]waf.Rule{{
		ID:         "block-payload",
		Expression: `request.body.contains("dangerous")`,
		Action:     waf.ActionBlock,
	}}))

	body := strings.NewReader("this is a very dangerous payload")
	req := httptest.NewRequest(http.MethodPost, "/", body)
	rr := httptest.NewRecorder()

	// Downstream must still be able to read the original body untouched.
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		_, _ = w.Write(got)
	})

	w.ServeHandler(downstream).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)

	// Now run a non-matching body and confirm the body is replayed downstream.
	w2 := waf.New()
	w2.InspectBody = 1024
	require.NoError(t, w2.SetRules([]waf.Rule{{
		ID: "noop", Expression: `request.body.contains("nope")`, Action: waf.ActionBlock,
	}}))
	req2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello world"))
	rr2 := httptest.NewRecorder()
	w2.ServeHandler(downstream).ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusOK, rr2.Code)
	assert.Equal(t, "hello world", rr2.Body.String())
}

func TestEmptyRuleset(t *testing.T) {
	t.Parallel()

	w := waf.New()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRequestCountry(t *testing.T) {
	t.Parallel()

	t.Run("blocks by resolved country", func(t *testing.T) {
		w := waf.New()
		w.Country = func(_ *http.Request) string { return "CN" }
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID: "block-cn", Expression: `request.country == "CN"`, Action: waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("resolver value passes through to a non-matching rule", func(t *testing.T) {
		w := waf.New()
		w.Country = func(_ *http.Request) string { return "TH" }
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID: "block-cn", Expression: `request.country == "CN"`, Action: waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("nil resolver leaves request.country empty without a missing-key error", func(t *testing.T) {
		// `request.country` is always present in the map, so a rule referencing
		// it evaluates cleanly (no fail-open) and just doesn't match.
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID: "block-cn", Expression: `request.country == "CN"`, Action: waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestRequestASN(t *testing.T) {
	t.Parallel()

	t.Run("blocks by resolved ASN", func(t *testing.T) {
		w := waf.New()
		w.ASN = func(_ *http.Request) int64 { return 13335 }
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID: "block-asn", Expression: `request.asn == 13335`, Action: waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("resolver value passes through to a non-matching rule", func(t *testing.T) {
		w := waf.New()
		w.ASN = func(_ *http.Request) int64 { return 15169 }
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID: "block-asn", Expression: `request.asn == 13335`, Action: waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("nil resolver leaves request.asn 0 without a missing-key error", func(t *testing.T) {
		// `request.asn` is always present in the map (0 when unset), so a rule
		// referencing it evaluates cleanly (no fail-open) and just doesn't match.
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID: "block-asn", Expression: `request.asn == 13335`, Action: waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("rule can match the unresolved sentinel explicitly", func(t *testing.T) {
		// request.asn == 0 is a usable predicate ("block traffic we can't
		// attribute to an AS"), proving the field is present even when unset.
		w := waf.New()
		require.NoError(t, w.SetRules([]waf.Rule{{
			ID: "block-unknown-asn", Expression: `request.asn == 0`, Action: waf.ActionBlock,
		}}))
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w.ServeHandler(passthroughHandler).ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestPriorityOrdering(t *testing.T) {
	t.Parallel()

	w := newWAF(t, []waf.Rule{
		{ID: "third", Expression: "true", Action: waf.ActionLog, Priority: 30},
		{ID: "first", Expression: "true", Action: waf.ActionLog, Priority: 10},
		{ID: "second", Expression: "true", Action: waf.ActionLog, Priority: 20},
	})
	assert.Equal(t, []string{"first", "second", "third"}, w.Rules())
}

// helpers

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
