package prom

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet/pkg/cache"
)

//nolint:govet
type cacheMetrics struct {
	once  sync.Once
	total *prometheus.CounterVec
	fill  *prometheus.HistogramVec
}

var _cache cacheMetrics

func (p *cacheMetrics) init() {
	p.once.Do(func() {
		p.total = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "cache_total",
		}, []string{"host", "result"})
		p.fill = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "cache_fill_duration_seconds",
			Buckets:   prometheus.DefBuckets,
		}, []string{"host"})
		reg.MustRegister(p.total, p.fill)
	})
}

func (p *cacheMetrics) observe(r *http.Request, info cache.ResultInfo) {
	if counter, err := p.total.GetMetricWith(prometheus.Labels{
		"host":   r.Host,
		"result": string(info.Result),
	}); err == nil {
		counter.Inc()
	}
	// FillDuration is non-zero only when the origin was actually contacted on the
	// serving path (a MISS or a stale-if-error attempt), so the histogram holds
	// origin-fill latency and is not diluted by instant cache hits.
	if info.FillDuration > 0 {
		if h, err := p.fill.GetMetricWith(prometheus.Labels{"host": r.Host}); err == nil {
			h.Observe(info.FillDuration.Seconds())
		}
	}
}

// Cache returns a cache.ResultFunc that records cache observability metrics on the
// shared registry, for wiring into cache.Options.OnResult:
//
//	store := cache.NewMemory(256 << 20)
//	c := cache.New(store, cache.Options{OnResult: prom.Cache()})
//
// It registers two metrics (lazily, once per process):
//
//	{namespace}_cache_total{host,result}             counter of cache outcomes
//	    (result = HIT|MISS|STALE|STALE_ERROR|BYPASS — a hit ratio is
//	     sum(HIT) / sum(all), the otherwise-invisible BYPASS path included)
//	{namespace}_cache_fill_duration_seconds{host}    histogram of origin-fill latency
//	    (observed only when the origin was contacted, i.e. MISS and stale-if-error)
//
// The host label matches prom.Requests so the two can be joined. Keeping the metric
// wiring here leaves pkg/cache free of any Prometheus dependency. Compose it with
// cache.LogResult to also emit a per-request cacheStatus log field:
//
//	metrics := prom.Cache()
//	c := cache.New(store, cache.Options{
//		OnResult: func(r *http.Request, info cache.ResultInfo) {
//			metrics(r, info)
//			cache.LogResult(r, info)
//		},
//	})
func Cache() cache.ResultFunc {
	_cache.init()
	return _cache.observe
}
