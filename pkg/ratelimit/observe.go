package ratelimit

// Result classifies the outcome of one rate-limit decision, reported via
// RateLimiter.Observe.
type Result uint8

const (
	// ResultAllowed: the request was admitted (Strategy.Take returned true). Under a
	// Redis fail-open this includes requests admitted because Redis was unreachable —
	// wire RedisFixedWindowStrategy.OnError (prom.RateLimitRedisError) to tell those
	// apart from genuinely-under-limit admits.
	ResultAllowed Result = iota
	// ResultLimited: the request was rejected (Strategy.Take returned false); the
	// ExceededHandler ran (a 429 by default).
	ResultLimited
)

// String renders a Result as a stable, bounded metric-label value.
func (r Result) String() string {
	switch r {
	case ResultAllowed:
		return "allowed"
	case ResultLimited:
		return "limited"
	default:
		return "unknown"
	}
}

// Event reports one rate-limit decision to RateLimiter.Observe. It carries only
// bounded fields: the operator-set limiter Name and the Result. It deliberately
// does NOT carry the client key, whose cardinality is unbounded and would blow up
// any per-key metric label set.
type Event struct {
	// Name is the operator-set RateLimiter.Name (may be ""), so several rate limiters
	// can be told apart in metrics. It is bounded by construction (operators set it).
	Name string
	// Result is allowed or limited.
	Result Result
}

// ObserveFunc is the rate-limit observation-hook shape, returned by prom.RateLimit
// for wiring into RateLimiter.Observe. It fires once per Take decision, IN ADDITION
// to (never replacing) ExceededHandler, on the request goroutine — keep it cheap.
type ObserveFunc func(Event)
