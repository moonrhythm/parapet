package prom

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet/pkg/upstream"
)

//nolint:govet
type upstreamStateMetrics struct {
	once        sync.Once
	state       *prometheus.GaugeVec
	transitions *prometheus.CounterVec
}

var _upstreamState upstreamStateMetrics

func (p *upstreamStateMetrics) init() {
	p.once.Do(func() {
		p.state = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "upstream_breaker_state",
		}, []string{"host"})
		p.transitions = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "upstream_state_transitions_total",
		}, []string{"host", "from", "to", "reason"})
		reg.MustRegister(p.state, p.transitions)
	})
}

func (p *upstreamStateMetrics) observe(c upstream.StateChange) {
	if g, err := p.state.GetMetricWith(prometheus.Labels{"host": c.Host}); err == nil {
		g.Set(float64(c.To)) // State's iota IS the gauge value: 0 closed / 1 open / 2 half_open
	}
	if ctr, err := p.transitions.GetMetricWith(prometheus.Labels{
		"host":   c.Host,
		"from":   c.From.String(),
		"to":     c.To.String(),
		"reason": c.Reason.String(),
	}); err == nil {
		ctr.Inc()
	}
}

// UpstreamState returns an upstream.StateChangeFunc that records per-target
// circuit-breaker / ejection state on the shared registry, for wiring into a
// reliability balancer's OnStateChange:
//
//	lb := upstream.NewCircuitBreakingLoadBalancer(targets)
//	lb.OnStateChange = prom.UpstreamState()
//	s.Use(upstream.New(lb))
//
// It registers two metrics (lazily, once per process):
//
//	{namespace}_upstream_breaker_state{host}                           gauge: 0 closed, 1 open, 2 half_open
//	{namespace}_upstream_state_transitions_total{host,from,to,reason}  counter of transitions
//
// The transitions counter is the authoritative signal: its increments are exact and
// order-independent, so alert on it. The gauge is a best-effort convenience — under
// concurrent transitions on the same host the emit-after-commit ordering can leave
// it momentarily, or until the next transition, showing a stale value; and for the
// ejecting balancers it reflects committed eject/recover events, not cooldown-expiry
// rotation membership (a target whose cooldown expired but has not yet served a
// successful request reads open). A target that has never transitioned has no gauge
// sample. The host label is the operator-configured upstream target (bounded).
func UpstreamState() upstream.StateChangeFunc {
	_upstreamState.init()
	return _upstreamState.observe
}
