package prom_test

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet/pkg/upstream"

	. "github.com/moonrhythm/parapet/pkg/prom"
)

func TestUpstream(t *testing.T) {
	observe := Upstream()
	require.NotNil(t, observe)

	// A unique target host isolates these assertions from any other test sharing the
	// process-global registry.
	const host = "prom-upstream-test.backend"
	r := httptest.NewRequest("GET", "/", nil)

	observe(r, upstream.RoundTripInfo{Host: host, Status: 200, Duration: 5 * time.Millisecond})
	observe(r, upstream.RoundTripInfo{Host: host, Status: 502, Duration: 3 * time.Millisecond})
	observe(r, upstream.RoundTripInfo{Host: host, Err: errors.New("dial fail"), Duration: time.Millisecond})

	assert.EqualValues(t, 1, counterValue(t, "parapet_upstream_requests", map[string]string{"host": host, "status": "200"}))
	assert.EqualValues(t, 1, counterValue(t, "parapet_upstream_requests", map[string]string{"host": host, "status": "502"}),
		"an origin 5xx is countable by status")
	assert.EqualValues(t, 1, counterValue(t, "parapet_upstream_requests", map[string]string{"host": host, "status": "error"}),
		"a transport failure is countable as error")

	// Every attempt — success or failure — contributes a TTFB sample.
	assert.EqualValues(t, 3, histogramCount(t, "parapet_upstream_request_duration_seconds", map[string]string{"host": host}))
}

// Per-backend origin metrics: request count by status and time-to-first-byte.
func ExampleUpstream() {
	lb := upstream.NewRoundRobinLoadBalancer([]*upstream.Target{
		{Host: "10.0.0.1:8080", Transport: &upstream.HTTPTransport{}},
		{Host: "10.0.0.2:8080", Transport: &upstream.HTTPTransport{}},
	})

	u := upstream.New(lb)
	u.OnRoundTrip = Upstream() // prom.Upstream(): count by host+status, observe TTFB
	_ = u                      // s.Use(u)
}
