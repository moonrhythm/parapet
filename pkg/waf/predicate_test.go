package waf_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet/pkg/waf"
)

func TestNewPredicate_Validation(t *testing.T) {
	t.Parallel()

	t.Run("empty expression rejected", func(t *testing.T) {
		_, err := waf.NewPredicate("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty expression")
	})

	t.Run("non-bool expression rejected", func(t *testing.T) {
		_, err := waf.NewPredicate(`request.method`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must return bool")
	})

	t.Run("syntax error rejected", func(t *testing.T) {
		_, err := waf.NewPredicate(`request.method ==`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "compile")
	})

	t.Run("unknown variable rejected at compile", func(t *testing.T) {
		_, err := waf.NewPredicate(`nope == "x"`)
		require.Error(t, err)
	})

	t.Run("valid expression compiles", func(t *testing.T) {
		p, err := waf.NewPredicate(`request.method == "POST"`)
		require.NoError(t, err)
		assert.Equal(t, `request.method == "POST"`, p.Expression())
	})
}

func TestPredicate_Eval(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		req  func() *http.Request
		want bool
	}{
		{
			name: "method match true",
			expr: `request.method == "POST"`,
			req:  func() *http.Request { return httptest.NewRequest(http.MethodPost, "/api", nil) },
			want: true,
		},
		{
			name: "method match false",
			expr: `request.method == "POST"`,
			req:  func() *http.Request { return httptest.NewRequest(http.MethodGet, "/api", nil) },
			want: false,
		},
		{
			name: "path prefix",
			expr: `request.path.startsWith("/api/")`,
			req:  func() *http.Request { return httptest.NewRequest(http.MethodGet, "/api/v1", nil) },
			want: true,
		},
		{
			name: "header lookup (lowercased key)",
			expr: `request.headers["x-api-key"] == "secret"`,
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("X-Api-Key", "secret")
				return r
			},
			want: true,
		},
		{
			name: "ipInCidr helper available",
			expr: `ipInCidr(request.remote_ip, "10.0.0.0/8")`,
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("X-Real-Ip", "10.1.2.3")
				return r
			},
			want: true,
		},
		{
			name: "regexMatch helper available",
			expr: `regexMatch(request.path, "^/admin")`,
			req:  func() *http.Request { return httptest.NewRequest(http.MethodGet, "/admin/x", nil) },
			want: true,
		},
		{
			name: "absent header tested with `in` idiom",
			expr: `!("x-absent" in request.headers)`,
			req:  func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) },
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := waf.NewPredicate(tc.expr)
			require.NoError(t, err)
			in := waf.NewInput(tc.req(), "", "", 0)
			got, err := p.Eval(context.Background(), in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPredicate_CountryAndASN(t *testing.T) {
	t.Parallel()

	p, err := waf.NewPredicate(`request.country == "TH" && request.asn == 13335`)
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	in := waf.NewInput(r, "", "TH", 13335)
	got, err := p.Eval(context.Background(), in)
	require.NoError(t, err)
	assert.True(t, got)

	// Unresolved geo (defaults) makes the same expression false, not an error.
	in2 := waf.NewInput(r, "", "", 0)
	got2, err := p.Eval(context.Background(), in2)
	require.NoError(t, err)
	assert.False(t, got2)
}

func TestPredicate_DisableMacros(t *testing.T) {
	t.Parallel()

	const expr = `request.headers.exists(k, k == "x-api-key")`
	// Macros enabled by default: compiles.
	_, err := waf.NewPredicate(expr)
	require.NoError(t, err)
	// Disabled: the macro is refused at compile.
	_, err = waf.NewPredicate(expr, waf.WithPredicateDisableMacros())
	require.Error(t, err)
}

func TestPredicate_InputReuse(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodPost, "/api/v1", nil)
	r.Header.Set("X-Api-Key", "k")
	in := waf.NewInput(r, "", "", 0)

	pMethod, err := waf.NewPredicate(`request.method == "POST"`)
	require.NoError(t, err)
	pPath, err := waf.NewPredicate(`request.path.startsWith("/api/")`)
	require.NoError(t, err)

	// One snapshot, evaluated by several predicates.
	gotM, err := pMethod.Eval(context.Background(), in)
	require.NoError(t, err)
	assert.True(t, gotM)
	gotP, err := pPath.Eval(context.Background(), in)
	require.NoError(t, err)
	assert.True(t, gotP)
}

func TestPredicate_CostLimit(t *testing.T) {
	t.Parallel()

	// A tiny cost limit makes even a trivial comparison exceed budget at eval.
	p, err := waf.NewPredicate(`request.path == "/x"`, waf.WithPredicateCostLimit(1))
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	_, err = p.Eval(context.Background(), waf.NewInput(r, "", "", 0))
	require.Error(t, err)
}

func TestPredicate_EvalTimeoutOptionAccepted(t *testing.T) {
	t.Parallel()

	// A configured eval timeout doesn't break a fast expression — the timeout
	// bounds runaway evaluation; deterministically forcing it would need an
	// expensive expression, which the cost-limit test already exercises.
	p, err := waf.NewPredicate(`request.path == "/x"`, waf.WithPredicateEvalTimeout(time.Second))
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	got, err := p.Eval(context.Background(), waf.NewInput(r, "", "", 0))
	require.NoError(t, err)
	assert.True(t, got)
}
