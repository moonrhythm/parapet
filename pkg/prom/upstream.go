package prom

import (
	"errors"
	"net/http"
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet/pkg/upstream"
)

//nolint:govet
type upstreamMetrics struct {
	once        sync.Once
	requests    *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	fastRejects *prometheus.CounterVec
}

var _upstream upstreamMetrics

func (p *upstreamMetrics) init() {
	p.once.Do(func() {
		p.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "upstream_requests",
		}, []string{"host", "status"})
		p.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "upstream_request_duration_seconds",
			Buckets:   prometheus.DefBuckets,
		}, []string{"host"})
		p.fastRejects = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "upstream_fast_rejects_total",
		}, []string{"host"})
		reg.MustRegister(p.requests, p.duration, p.fastRejects)
	})
}

func (p *upstreamMetrics) observe(_ *http.Request, info upstream.RoundTripInfo) {
	// A transport-level failure (no response) is labelled "error"; otherwise the
	// origin's numeric status, so 5xx counts fall out of the same metric.
	status := strconv.Itoa(info.Status)
	if info.Err != nil {
		status = "error"
	}
	if c, err := p.requests.GetMetricWith(prometheus.Labels{
		"host":   info.Host,
		"status": status,
	}); err == nil {
		c.Inc()
	}
	if h, err := p.duration.GetMetricWith(prometheus.Labels{"host": info.Host}); err == nil {
		h.Observe(info.Duration.Seconds())
	}
	// Fast-reject: a reliability balancer shed the request before any round-trip
	// (Upstream.RoundTrip then leaves host empty). errors.Is so a wrapped
	// ErrUnavailable still counts.
	if errors.Is(info.Err, upstream.ErrUnavailable) {
		if c, err := p.fastRejects.GetMetricWith(prometheus.Labels{"host": info.Host}); err == nil {
			c.Inc()
		}
	}
}

// Upstream returns an upstream.RoundTripFunc that records per-backend origin
// metrics on the shared registry, for wiring into Upstream.OnRoundTrip:
//
//	u := upstream.New(lb)
//	u.OnRoundTrip = prom.Upstream()
//	s.Use(u)
//
// It registers two metrics (lazily, once per process):
//
//	{namespace}_upstream_requests{host,status}                  counter of attempts
//	    (status = the origin's numeric code, or "error" for a transport failure;
//	     an origin-error rate is sum(status=~"5..") + sum(status="error"))
//	{namespace}_upstream_request_duration_seconds{host}         histogram of TTFB
//	    (transport round-trip latency: connect + send + time to response headers)
//	{namespace}_upstream_fast_rejects_total{host}               counter of requests
//	    a reliability balancer shed before any round-trip (ErrUnavailable). The host
//	    is "" for a shed before any pick; pair with prom.UpstreamState for circuit
//	    and ejection state.
//
// It fires once per attempt, so retries are counted individually. The host label is
// the resolved upstream target (operator-configured, bounded), distinct from the
// client-facing host of prom.Requests. Keeping the wiring here leaves pkg/upstream
// free of any Prometheus dependency.
func Upstream() upstream.RoundTripFunc {
	_upstream.init()
	return _upstream.observe
}
