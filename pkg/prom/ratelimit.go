package prom

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/moonrhythm/parapet/pkg/ratelimit"
)

//nolint:govet
type rateLimitMetrics struct {
	once       sync.Once
	decisions  *prometheus.CounterVec
	redisError prometheus.Counter
}

var _rateLimit rateLimitMetrics

func (p *rateLimitMetrics) init() {
	p.once.Do(func() {
		p.decisions = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "ratelimit_total",
		}, []string{"name", "result"})
		p.redisError = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "ratelimit_redis_errors_total",
		})
		reg.MustRegister(p.decisions, p.redisError)
	})
}

func (p *rateLimitMetrics) observe(e ratelimit.Event) {
	if c, err := p.decisions.GetMetricWith(prometheus.Labels{
		"name":   e.Name,
		"result": e.Result.String(),
	}); err == nil {
		c.Inc()
	}
}

func (p *rateLimitMetrics) observeRedisError(error) {
	p.redisError.Inc()
}

// RateLimit returns a ratelimit.ObserveFunc that records rate-limit decisions on the
// shared registry, for wiring into RateLimiter.Observe — keeping pkg/ratelimit
// Prometheus-free (the prom.Mirror/prom.UpstreamState convention).
//
//	rl := ratelimit.FixedWindowPerSecond(100)
//	rl.Name = "api"            // bounded label; "" is fine
//	rl.Observe = prom.RateLimit()
//	s.Use(rl)
//
// It records one metric (lazily, once per process):
//
//	{namespace}_ratelimit_total{name,result}  counter, result = allowed | limited
//
// The hook is NON-replacing: it fires on every Take decision IN ADDITION to
// ExceededHandler, so an operator can count rejections (result="limited") without
// reimplementing the 429 response. Both labels are bounded — name is operator-set
// (RateLimiter.Name) and result is the two-value closed set; the client key, whose
// cardinality is unbounded, is never a label.
//
// IMPORTANT (the silent fail-open): a RedisFixedWindowStrategy with FailOpen=true
// (the constructor default) ADMITS every request when Redis is down — and those
// admits land in result="allowed", indistinguishable here from genuinely-under-limit
// admits. Also wire RateLimitRedisError into RedisFixedWindowStrategy.OnError to make
// Redis-down a distinct, alertable series.
func RateLimit() ratelimit.ObserveFunc {
	_rateLimit.init()
	return _rateLimit.observe
}

// RateLimitRedisError returns a func(error) for wiring into
// RedisFixedWindowStrategy.OnError, so a swallowed Redis error (timeout, dial, script
// error) — which RedisFixedWindowStrategy.Take folds into the FailOpen bool and the
// RateLimiter therefore cannot see — surfaces as a distinct counter on the shared
// registry:
//
//	{namespace}_ratelimit_redis_errors_total  counter
//
//	s := &ratelimit.RedisFixedWindowStrategy{Runner: r, Max: 100, Size: time.Second, FailOpen: true}
//	s.OnError = prom.RateLimitRedisError()
//	rl := ratelimit.New(s)
//	rl.Observe = prom.RateLimit()
//
// It is a SEPARATE counter rather than a result="error" row on ratelimit_total by
// design: the OnError hook fires on the strategy, which has no access to the
// RateLimiter.Name, and at a different granularity (once per failed Redis op, while a
// fail-open request ALSO increments result="allowed") — folding the two together
// would carry a permanently-empty name label and double-count the request. As its own
// series it stays honest: when Redis degrades it climbs while result="limited" stays
// flat, which is exactly the otherwise-invisible silent-admit signal to alert on. The
// error value is intentionally not labelled (unbounded). Optional and zero-cost when
// unset (a nil OnError is never called).
func RateLimitRedisError() func(error) {
	_rateLimit.init()
	return _rateLimit.observeRedisError
}
