package prom

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet/pkg/mirror"
)

//nolint:govet
type mirrorMetrics struct {
	once     sync.Once
	outcomes *prometheus.CounterVec
	duration prometheus.Histogram
}

var _mirror mirrorMetrics

func (p *mirrorMetrics) init() {
	p.once.Do(func() {
		p.outcomes = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "mirror_total",
		}, []string{"outcome"})
		p.duration = prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "mirror_request_duration_seconds",
			Buckets:   prometheus.DefBuckets,
		})
		reg.MustRegister(p.outcomes, p.duration)
	})
}

func outcomeLabel(o mirror.Outcome) string {
	switch o {
	case mirror.OutcomeDispatched:
		return "dispatched"
	case mirror.OutcomeCompleted:
		return "completed"
	case mirror.OutcomeDroppedFull:
		return "dropped_full"
	case mirror.OutcomeDroppedOversize:
		return "dropped_oversize"
	case mirror.OutcomePanicked:
		return "panicked"
	default:
		return "unknown"
	}
}

func (p *mirrorMetrics) observe(info mirror.MirrorInfo) {
	if c, err := p.outcomes.GetMetricWith(prometheus.Labels{"outcome": outcomeLabel(info.Outcome)}); err == nil {
		c.Inc()
	}
	if info.Outcome == mirror.OutcomeCompleted {
		p.duration.Observe(info.Duration.Seconds())
	}
}

// Mirror returns a mirror.MirrorFunc that records shadow-traffic metrics on the
// shared registry, for wiring into Mirror.Observe — keeping pkg/mirror
// Prometheus-free (the prom.Upstream convention). mirror_total{outcome} counts each
// decision/result; mirror_request_duration_seconds observes completed round-trips.
func Mirror() mirror.MirrorFunc {
	_mirror.init()
	return _mirror.observe
}
