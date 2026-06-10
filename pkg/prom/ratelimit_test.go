package prom_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet/pkg/ratelimit"

	. "github.com/moonrhythm/parapet/pkg/prom"
)

func TestRateLimit(t *testing.T) {
	observe := RateLimit()
	require.NotNil(t, observe)
	onError := RateLimitRedisError()
	require.NotNil(t, onError)

	// A unique name isolates these assertions from any other test sharing the
	// process-global registry, and baselines keep them order- and -count-independent.
	const name = "prom-ratelimit-test"
	baseAllowed := baseline(t, "parapet_ratelimit_total", map[string]string{"name": name, "result": "allowed"})
	baseLimited := baseline(t, "parapet_ratelimit_total", map[string]string{"name": name, "result": "limited"})
	baseErrors := baseline(t, "parapet_ratelimit_redis_errors_total", nil)

	observe(ratelimit.Event{Name: name, Result: ratelimit.ResultAllowed})
	observe(ratelimit.Event{Name: name, Result: ratelimit.ResultLimited})
	observe(ratelimit.Event{Name: name, Result: ratelimit.ResultLimited})
	onError(errors.New("redis down"))

	assert.EqualValues(t, baseAllowed+1, counterValue(t, "parapet_ratelimit_total",
		map[string]string{"name": name, "result": "allowed"}), "one allowed decision counted")
	assert.EqualValues(t, baseLimited+2, counterValue(t, "parapet_ratelimit_total",
		map[string]string{"name": name, "result": "limited"}), "two limited decisions counted")
	assert.EqualValues(t, baseErrors+1, counterValue(t, "parapet_ratelimit_redis_errors_total", nil),
		"the swallowed redis error surfaces as its own series, not a silent admit")
}

// baseline reads the current value of a counter series, treating an absent series
// (counterValue returns -1) as 0, so delta assertions survive the process-global
// registry across tests and -count reruns.
func baseline(t *testing.T, name string, want map[string]string) float64 {
	t.Helper()
	v := counterValue(t, name, want)
	if v < 0 {
		return 0
	}
	return v
}

func ExampleRateLimit() {
	rl := ratelimit.FixedWindowPerSecond(100)
	rl.Name = "api"            // bounded label carried on every Event; "" is fine
	rl.Observe = RateLimit()   // prom.RateLimit(): count decisions by name+result
	_ = rl                     // s.Use(rl)
}

func ExampleRateLimitRedisError() {
	s := &ratelimit.RedisFixedWindowStrategy{Max: 100, Size: time.Second, FailOpen: true}
	s.OnError = RateLimitRedisError() // count Redis-down so silent fail-open admits are visible
	rl := ratelimit.New(s)
	rl.Name = "api"
	rl.Observe = RateLimit()
	_ = rl // s.Use(rl)
}
