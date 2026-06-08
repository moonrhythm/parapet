package prom_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet/pkg/cache"
	. "github.com/moonrhythm/parapet/pkg/prom"
)

// counterValue returns the gathered counter sample whose labels include want, or
// -1 if absent — found without naming the prometheus client_model types.
func counterValue(t *testing.T, name string, want map[string]string) float64 {
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
			if subset(want, got) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return -1
}

func histogramCount(t *testing.T, name string, want map[string]string) uint64 {
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
			if subset(want, got) {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func subset(want, got map[string]string) bool {
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func TestCache(t *testing.T) {
	observe := Cache()
	require.NotNil(t, observe)

	// A unique host isolates these assertions from any other test sharing the
	// process-global registry.
	const host = "prom-cache-test.example"
	r := httptest.NewRequest("GET", "http://"+host+"/x", nil)

	observe(r, cache.ResultInfo{Result: cache.ResultHit})
	observe(r, cache.ResultInfo{Result: cache.ResultMiss, FillDuration: 7 * time.Millisecond})
	observe(r, cache.ResultInfo{Result: cache.ResultBypass})

	assert.EqualValues(t, 1, counterValue(t, "parapet_cache_total", map[string]string{"host": host, "result": "HIT"}))
	assert.EqualValues(t, 1, counterValue(t, "parapet_cache_total", map[string]string{"host": host, "result": "MISS"}))
	assert.EqualValues(t, 1, counterValue(t, "parapet_cache_total", map[string]string{"host": host, "result": "BYPASS"}),
		"the otherwise-invisible bypass path is counted")

	// Only the MISS carried a fill duration, so the histogram saw exactly one sample.
	assert.EqualValues(t, 1, histogramCount(t, "parapet_cache_fill_duration_seconds", map[string]string{"host": host}),
		"a hit/bypass contributes no fill-latency sample")
}

// Cache observability: count outcomes and fill latency, and tag access logs.
func ExampleCache() {
	store := cache.NewMemory(256 << 20)

	metrics := Cache() // prom.Cache()
	_ = cache.New(store, cache.Options{
		// Compose metrics with the cacheStatus log field; pass prom.Cache() alone if
		// you only want metrics.
		OnResult: func(r *http.Request, info cache.ResultInfo) {
			metrics(r, info)
			cache.LogResult(r, info)
		},
	})
}
