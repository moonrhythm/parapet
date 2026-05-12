package waf_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moonrhythm/parapet/pkg/waf"
)

// noopHandler is the cheapest possible downstream so benchmark output is
// dominated by WAF evaluation cost.
var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func benchHandler(b *testing.B, rules []waf.Rule) http.Handler {
	b.Helper()
	w := waf.New()
	if err := w.SetRules(rules); err != nil {
		b.Fatalf("SetRules: %v", err)
	}
	return w.ServeHandler(noopHandler)
}

// BenchmarkNoRules measures the cost of the empty-ruleset fast path.
// This is the floor: any non-zero number is pure middleware overhead.
func BenchmarkNoRules(b *testing.B) {
	h := waf.New().ServeHandler(noopHandler)
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}
}

// BenchmarkSingleSimpleRule measures the most common WAF rule shape:
// a path prefix check that does not match.
func BenchmarkSingleSimpleRule(b *testing.B) {
	h := benchHandler(b, []waf.Rule{{
		ID: "block-admin", Expression: `request.path.startsWith("/admin")`, Action: waf.ActionBlock,
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}
}

// BenchmarkRegexRule exercises the regex cache.
func BenchmarkRegexRule(b *testing.B) {
	h := benchHandler(b, []waf.Rule{{
		ID:         "block-sqli",
		Expression: `regexMatch(lower(request.query), "(union\\s+select|or\\s+1=1)")`,
		Action:     waf.ActionBlock,
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/users?q=hello", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}
}

// BenchmarkTenRules approximates a realistic ruleset: ten cheap checks
// that all miss, so every rule must be evaluated.
func BenchmarkTenRules(b *testing.B) {
	rules := []waf.Rule{
		{ID: "r1", Expression: `request.path.startsWith("/admin")`, Action: waf.ActionBlock},
		{ID: "r2", Expression: `request.path.startsWith("/.git")`, Action: waf.ActionBlock},
		{ID: "r3", Expression: `request.path.startsWith("/.env")`, Action: waf.ActionBlock},
		{ID: "r4", Expression: `request.method == "TRACE"`, Action: waf.ActionBlock},
		{ID: "r5", Expression: `request.headers["x-bad"] == "1"`, Action: waf.ActionBlock},
		{ID: "r6", Expression: `containsAny(lower(request.user_agent), ["sqlmap", "nikto", "acunetix"])`, Action: waf.ActionBlock},
		{ID: "r7", Expression: `request.content_length > 10000000`, Action: waf.ActionBlock},
		{ID: "r8", Expression: `ipInCidr(request.remote_ip, "192.0.2.0/24")`, Action: waf.ActionBlock},
		{ID: "r9", Expression: `request.args["debug"] == "1"`, Action: waf.ActionBlock},
		{ID: "r10", Expression: `request.path.contains("../")`, Action: waf.ActionBlock},
	}
	h := benchHandler(b, rules)
	req := httptest.NewRequest(http.MethodGet, "/api/users?id=42", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 Chrome")
	req.Header.Set("X-Real-Ip", "8.8.8.8")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}
}

// BenchmarkParallel100Rules ensures the WAF scales linearly with cores —
// the compiled cel.Programs and the atomic ruleset pointer must not
// serialise traffic.
func BenchmarkParallel100Rules(b *testing.B) {
	rules := make([]waf.Rule, 100)
	for i := range rules {
		rules[i] = waf.Rule{
			ID:         "r" + itoa(i),
			Expression: `request.path.startsWith("/route" + "` + itoa(i) + `")`,
			Action:     waf.ActionBlock,
		}
	}
	h := benchHandler(b, rules)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
		for pb.Next() {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
		}
	})
}

// BenchmarkSetRules measures the cost of compiling and atomically swapping
// a ruleset — this is the cost paid by an admin endpoint pushing new rules,
// NOT by the request path.
func BenchmarkSetRules(b *testing.B) {
	w := waf.New()
	rules := []waf.Rule{
		{ID: "r1", Expression: `request.path.startsWith("/admin")`, Action: waf.ActionBlock},
		{ID: "r2", Expression: `regexMatch(request.user_agent, "(?i)sqlmap|nikto")`, Action: waf.ActionBlock},
		{ID: "r3", Expression: `ipInCidr(request.remote_ip, "10.0.0.0/8")`, Action: waf.ActionAllow},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := w.SetRules(rules); err != nil {
			b.Fatal(err)
		}
	}
}
