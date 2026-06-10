package prom_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet/pkg/waf"

	. "github.com/moonrhythm/parapet/pkg/prom"
)

// bucketCount returns the cumulative count of the histogram series matching want
// at the bucket whose upper bound == le, or 0 if absent. Like the other prom
// helpers it reads the process-global registry, so callers baseline-then-delta.
func bucketCount(t *testing.T, name string, want map[string]string, le float64) uint64 {
	t.Helper()
	mfs, err := Registry().Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, p := range m.GetLabel() {
				got[p.GetName()] = p.GetValue()
			}
			if !subset(want, got) {
				continue
			}
			for _, b := range m.GetHistogram().GetBucket() {
				if b.GetUpperBound() == le {
					return b.GetCumulativeCount()
				}
			}
		}
	}
	return 0
}

func TestWAF(t *testing.T) {
	observe := WAF()
	require.NotNil(t, observe)

	const name = "parapet_waf_eval_duration_seconds"
	outcomes := map[waf.Outcome]string{
		waf.OutcomePass:  "pass",
		waf.OutcomeAllow: "allow",
		waf.OutcomeBlock: "block",
		waf.OutcomeError: "error",
	}
	for o, label := range outcomes {
		base := histogramCount(t, name, map[string]string{"outcome": label})
		observe(waf.EvalEvent{Outcome: o, Duration: time.Millisecond})
		got := histogramCount(t, name, map[string]string{"outcome": label})
		assert.Equal(t, base+1, got, "outcome %q records into its own series exactly once", label)
	}
}

func TestWAF_BucketResolution(t *testing.T) {
	observe := WAF()
	lbl := map[string]string{"outcome": "pass"}

	// Fine sub-ms buckets discriminate a 30us eval from a 6ms eval — resolution
	// DefBuckets (smallest boundary 5ms) cannot provide. This guards against a
	// regression that swaps wafEvalBuckets back to DefBuckets.
	base500us := bucketCount(t, "parapet_waf_eval_duration_seconds", lbl, 0.0005)
	base5ms := bucketCount(t, "parapet_waf_eval_duration_seconds", lbl, 0.005)
	base10ms := bucketCount(t, "parapet_waf_eval_duration_seconds", lbl, 0.01)

	observe(waf.EvalEvent{Outcome: waf.OutcomePass, Duration: 30 * time.Microsecond})
	observe(waf.EvalEvent{Outcome: waf.OutcomePass, Duration: 6 * time.Millisecond})

	assert.Equal(t, base500us+1, bucketCount(t, "parapet_waf_eval_duration_seconds", lbl, 0.0005),
		"only the 30us sample is <= 500us")
	assert.Equal(t, base5ms+1, bucketCount(t, "parapet_waf_eval_duration_seconds", lbl, 0.005),
		"the 6ms sample is above the 5ms SLO-line bucket")
	assert.Equal(t, base10ms+2, bucketCount(t, "parapet_waf_eval_duration_seconds", lbl, 0.01),
		"both samples are <= 10ms")
}

// WAF observability: a per-request rule-eval latency histogram, split by outcome
// (pass|allow|block|error), so a dashboard can show whether the WAF is adding
// tail latency and on which path.
func ExampleWAF() {
	w := waf.New()
	w.Observe = WAF() // prom.WAF(): records parapet_waf_eval_duration_seconds{outcome}
	_ = w             // s.Use(w)
}
