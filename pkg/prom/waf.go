package prom

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet/pkg/waf"
)

//nolint:govet
type wafMetrics struct {
	once sync.Once
	eval *prometheus.HistogramVec
}

var _waf wafMetrics

// wafEvalBuckets are tuned for WAF rule-evaluation latency, which runs from
// sub-microsecond (a single cheap string compare) up to WAF.EvalTimeout
// (default 5ms). prometheus.DefBuckets is useless here: its smallest boundary is
// 5ms, so against the default timeout nearly every eval lands in the first
// (le="0.005") bucket and the histogram has zero resolution. These boundaries
// are dense below 1ms (where the bulk of evals sit, so a p99 regression from a
// newly-added expensive rule actually shows), place an edge ON the 5ms default
// timeout (the dashboard SLO line — samples above it are brushing the deadline,
// which under FailClosed turn into 500s), and keep 10ms/25ms headroom so raised
// timeouts or FailOpen-swallowed slow rules don't collapse the whole tail into
// +Inf and hide the "is the WAF adding tail latency" signal.
var wafEvalBuckets = []float64{
	0.000025, // 25us
	0.00005,  // 50us
	0.0001,   // 100us
	0.00025,  // 250us
	0.0005,   // 500us
	0.001,    // 1ms
	0.0025,   // 2.5ms
	0.005,    // 5ms  — default EvalTimeout (SLO line)
	0.01,     // 10ms — past-timeout tail
	0.025,    // 25ms — raised-timeout / FailOpen slow-rule headroom
}

func (p *wafMetrics) init() {
	p.once.Do(func() {
		p.eval = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "waf_eval_duration_seconds",
			Buckets:   wafEvalBuckets,
		}, []string{"outcome"})
		reg.MustRegister(p.eval)
	})
}

func (p *wafMetrics) observe(ev waf.EvalEvent) {
	if h, err := p.eval.GetMetricWith(prometheus.Labels{
		"outcome": ev.Outcome.String(),
	}); err == nil {
		h.Observe(ev.Duration.Seconds())
	}
}

// WAF returns a waf.ObserveFunc that records per-request WAF rule-evaluation
// latency on the shared registry, for wiring into WAF.Observe — keeping pkg/waf
// Prometheus-free (the prom.Mirror / prom.Cache convention):
//
//	w := waf.New()
//	w.Observe = prom.WAF()
//
// It registers one metric (lazily, once per process):
//
//	{namespace}_waf_eval_duration_seconds{outcome}   histogram of rule-eval latency
//	    (outcome = pass|allow|block|error — pass is the no-match fall-through
//	     plus log-only matches plus FailOpen-swallowed errors; error is only a
//	     FailClosed terminating error). The span is rule evaluation ONLY: it
//	     excludes client body buffering and geo/ASN/request-map construction,
//	     matching waf.MatchEvent.Elapsed; the no-rules fast path is not recorded.
//	     Each outcome's _count child doubles as a per-outcome request rate, so no
//	     separate counter is registered.
//
// Buckets are sub-ms..25ms, sized to the default 5ms EvalTimeout (DefBuckets
// would collapse the whole distribution into one bucket). A per-outcome
// tail-latency panel is:
//
//	histogram_quantile(0.99, sum by (le, outcome)
//	  (rate(parapet_waf_eval_duration_seconds_bucket[5m])))
func WAF() waf.ObserveFunc {
	_waf.init()
	return _waf.observe
}
