package prom_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet/pkg/upstream"

	. "github.com/moonrhythm/parapet/pkg/prom"
)

func TestUpstreamShed(t *testing.T) {
	observe := UpstreamShed()
	require.NotNil(t, observe)

	for _, r := range []upstream.ShedReason{upstream.ShedSaturated, upstream.ShedEmpty, upstream.ShedAllDark} {
		lbl := map[string]string{"reason": r.String()}
		base := counterValue(t, "parapet_upstream_shed_total", lbl)
		observe(r)
		got := counterValue(t, "parapet_upstream_shed_total", lbl)
		assert.EqualValues(t, 1, countDelta(base, got), "shed reason %q records into its own series once", r.String())
	}
}

// noopRT is a transport that never actually serves — the inflight test only reads the
// gauge from configured state, with no traffic.
type noopRT struct{}

func (noopRT) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}}, nil
}

// registered once across -count runs (the underlying collector registers once; a
// second registration of the same balancer/hosts would duplicate the scrape).
var inflightOnce sync.Once

func TestUpstreamInflight(t *testing.T) {
	inflightOnce.Do(func() {
		lb := upstream.NewLeastConnLoadBalancer([]*upstream.Target{
			{Host: "prom-inflight-bounded", Transport: noopRT{}, MaxConcurrent: 5},
			{Host: "prom-inflight-unbounded", Transport: noopRT{}}, // Cap 0
		})
		UpstreamInflight(lb)
	})

	// No traffic: inflight 0 for both; the bounded target has a capacity series, the
	// unbounded one does not (gaugeValue returns -1 for an absent series).
	assert.EqualValues(t, 0, gaugeValue(t, "parapet_upstream_inflight",
		map[string]string{"host": "prom-inflight-bounded"}))
	assert.EqualValues(t, 5, gaugeValue(t, "parapet_upstream_inflight_capacity",
		map[string]string{"host": "prom-inflight-bounded"}))
	assert.EqualValues(t, 0, gaugeValue(t, "parapet_upstream_inflight",
		map[string]string{"host": "prom-inflight-unbounded"}))
	assert.EqualValues(t, -1, gaugeValue(t, "parapet_upstream_inflight_capacity",
		map[string]string{"host": "prom-inflight-unbounded"}),
		"an unbounded target emits no capacity series")
}

// registered once across -count runs.
var dedupOnce sync.Once

// The load-bearing guarantee of the single-global-collector design: registering the
// SAME host via two balancers must NOT produce a duplicate label set (which fails the
// whole scrape's Gather with "collected before with the same label values"). The
// collector dedups by host, first writer wins.
func TestUpstreamInflight_DedupSameHost(t *testing.T) {
	dedupOnce.Do(func() {
		lb1 := upstream.NewLeastConnLoadBalancer([]*upstream.Target{
			{Host: "prom-inflight-dup", Transport: noopRT{}, MaxConcurrent: 7},
		})
		lb2 := upstream.NewLeastConnLoadBalancer([]*upstream.Target{
			{Host: "prom-inflight-dup", Transport: noopRT{}, MaxConcurrent: 99},
		})
		UpstreamInflight(lb1)
		UpstreamInflight(lb2)
	})

	// gaugeValue's internal Registry().Gather() require.NoError fails if the dedup is
	// removed (the duplicate series errors the scrape). First writer (lb1) wins.
	assert.EqualValues(t, 0, gaugeValue(t, "parapet_upstream_inflight",
		map[string]string{"host": "prom-inflight-dup"}))
	assert.EqualValues(t, 7, gaugeValue(t, "parapet_upstream_inflight_capacity",
		map[string]string{"host": "prom-inflight-dup"}),
		"a duplicate host dedups to the first writer; the scrape stays valid")
}

// Bulkhead saturation observability: a live per-target in-flight gauge plus a
// shed-by-cause counter, so a dashboard shows which target is pinned at its cap and
// whether the balancer is shedding because it's saturated vs an empty/dark pool.
func ExampleUpstreamInflight() {
	lb := upstream.NewLeastConnLoadBalancer([]*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: &upstream.HTTPTransport{}, MaxConcurrent: 100},
		{Host: "10.0.0.2:8080", Transport: &upstream.HTTPTransport{}, MaxConcurrent: 100},
	})
	UpstreamInflight(lb)       // gauges: parapet_upstream_inflight{host} + _capacity{host}
	lb.OnShed = UpstreamShed() // counter: parapet_upstream_shed_total{reason}
	_ = lb                     // s.Use(upstream.New(lb))
}
