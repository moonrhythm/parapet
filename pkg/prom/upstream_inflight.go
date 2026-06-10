package prom

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet/pkg/upstream"
)

// --- (1) per-target in-flight gauge: a scrape-time Collector, zero hot-path cost ---

// inflightMetrics is a single process-global scrape-time Collector fed by a list of
// LeastConn balancers. One collector (registered once) rather than one-per-balancer:
// two collectors exporting the same Desc would panic at MustRegister, so a list lets
// an operator observe several upstream pools and keeps the test and the example from
// colliding.
//
//nolint:govet // fields grouped for readability, not pointer-packing
type inflightMetrics struct {
	once     sync.Once
	inflight *prometheus.Desc
	capacity *prometheus.Desc
	mu       sync.Mutex
	lbs      []*upstream.LeastConnLoadBalancer
}

var _upstreamInflight inflightMetrics

func (p *inflightMetrics) init() {
	p.once.Do(func() {
		p.inflight = prometheus.NewDesc(
			Namespace+"_upstream_inflight",
			"Current in-flight requests per least-conn target (bulkhead occupancy).",
			[]string{"host"}, nil)
		p.capacity = prometheus.NewDesc(
			Namespace+"_upstream_inflight_capacity",
			"MaxConcurrent bulkhead cap per least-conn target (bounded targets only).",
			[]string{"host"}, nil)
		reg.MustRegister(p)
	})
}

func (p *inflightMetrics) add(lb *upstream.LeastConnLoadBalancer) {
	p.mu.Lock()
	p.lbs = append(p.lbs, lb)
	p.mu.Unlock()
}

func (p *inflightMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- p.inflight
	ch <- p.capacity
}

func (p *inflightMetrics) Collect(ch chan<- prometheus.Metric) {
	p.mu.Lock()
	lbs := append([]*upstream.LeastConnLoadBalancer(nil), p.lbs...)
	p.mu.Unlock()
	// Dedup by host: a host registered twice (a re-registered balancer, or one host
	// appearing in two pools) would otherwise emit a duplicate label set and fail the
	// scrape's Gather. First writer wins.
	seen := make(map[string]struct{})
	for _, lb := range lbs {
		for _, t := range lb.Inflight() { // live atomic snapshot, read at scrape time
			if _, dup := seen[t.Host]; dup {
				continue
			}
			seen[t.Host] = struct{}{}
			ch <- prometheus.MustNewConstMetric(p.inflight, prometheus.GaugeValue, float64(t.Active), t.Host)
			// Emit capacity only for bounded targets: an unbounded target (Cap==0) has
			// no saturation point, and a cap=0 series would mislead an inflight/capacity
			// panel.
			if t.Cap > 0 {
				ch <- prometheus.MustNewConstMetric(p.capacity, prometheus.GaugeValue, float64(t.Cap), t.Host)
			}
		}
	}
}

// UpstreamInflight registers a LeastConnLoadBalancer with a scrape-time collector
// that exports its live bulkhead occupancy on the shared registry:
//
//	lb := upstream.NewLeastConnLoadBalancer(targets)
//	prom.UpstreamInflight(lb)
//	s.Use(upstream.New(lb))
//
// It exports two gauges, both read live at scrape time from lb.Inflight() — nothing
// runs on the claim/dec hot path:
//
//	{namespace}_upstream_inflight{host}           in-flight requests on the target right now
//	{namespace}_upstream_inflight_capacity{host}  the target's MaxConcurrent cap (bounded targets only)
//
// Saturation of a target is inflight/inflight_capacity; a target pinned at 1.0 is the
// one driving any upstream_shed_total{reason="saturated"} (see prom.UpstreamShed). The
// host label is the operator-configured upstream target (bounded). Call it for each
// LeastConn pool you want observed; the single underlying collector is registered once
// per process. Host labels must be unique across registered pools: if the same host
// appears in two pools the collector dedups it (first registered pool wins, INCLUDING
// its inflight and capacity values) to keep the scrape valid. Keeping the wiring here
// leaves pkg/upstream free of any Prometheus dependency.
func UpstreamInflight(lb *upstream.LeastConnLoadBalancer) {
	_upstreamInflight.init()
	_upstreamInflight.add(lb)
}

// --- (2) shed counter: a cheap push hook ---

//nolint:govet
type upstreamShedMetrics struct {
	once  sync.Once
	sheds *prometheus.CounterVec
}

var _upstreamShed upstreamShedMetrics

func (p *upstreamShedMetrics) init() {
	p.once.Do(func() {
		p.sheds = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "upstream_shed_total",
		}, []string{"reason"})
		reg.MustRegister(p.sheds)
	})
}

func (p *upstreamShedMetrics) observe(reason upstream.ShedReason) {
	if c, err := p.sheds.GetMetricWith(prometheus.Labels{"reason": reason.String()}); err == nil {
		c.Inc()
	}
}

// UpstreamShed returns an upstream.ShedFunc that counts LeastConnLoadBalancer sheds
// on the shared registry, for wiring into LeastConnLoadBalancer.OnShed:
//
//	lb := upstream.NewLeastConnLoadBalancer(targets)
//	lb.OnShed = prom.UpstreamShed()
//	s.Use(upstream.New(lb))
//
// It registers one metric (lazily, once per process):
//
//	{namespace}_upstream_shed_total{reason}  counter of pre-round-trip sheds, reason:
//	    "saturated" — every gate-up target was at its MaxConcurrent cap (the brownout)
//	    "empty"     — the pool had no targets configured
//	    "all_dark"  — the active-HC gate marked the whole pool down
//
// This disambiguates a capacity brownout (reason="saturated") from a dead/empty pool
// (reason="empty"/"all_dark") and from a circuit-breaker all-open shed (a different
// balancer, reported via prom.UpstreamState) — all of which otherwise collapse into
// prom.Upstream's host-less upstream_fast_rejects_total{host=""}. Alert on the RATE of
// reason="saturated" to catch sustained bulkhead overload. The reason label is a
// closed three-value set, so the series can never grow unbounded. The wiring here
// leaves pkg/upstream free of any Prometheus dependency.
func UpstreamShed() upstream.ShedFunc {
	_upstreamShed.init()
	return _upstreamShed.observe
}
