package upstream

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// recordingTransport records the resolved host of every round-trip, across all
// targets that share it. Safe for concurrent use. Shared by the weighted and
// least-connection tests.
type recordingTransport struct {
	mu   sync.Mutex
	hits []string
}

func (t *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.hits = append(t.hits, r.URL.Host)
	t.mu.Unlock()
	return httptest.NewRecorder().Result(), nil
}

func (t *recordingTransport) counts() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := map[string]int{}
	for _, h := range t.hits {
		m[h]++
	}
	return m
}

// driveLB issues n requests through a balancer, closing each response body so an
// in-flight count (least-conn) is released between calls.
func driveLB(l http.RoundTripper, n int) {
	for range n {
		resp, _ := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}
}

// weightedTargets builds targets host "t0","t1",... sharing one recording transport.
func weightedTargets(rec *recordingTransport, weights ...int) []*Target {
	targets := make([]*Target, len(weights))
	for i, w := range weights {
		targets[i] = &Target{Host: fmt.Sprintf("t%d", i), Transport: rec, Weight: w}
	}
	return targets
}

func TestWeightedRoundRobinLoadBalancer(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		l := NewWeightedRoundRobinLoadBalancer(nil)
		resp, err := l.RoundTrip(httptest.NewRequest("GET", "/", nil))
		assert.Nil(t, resp)
		assert.Equal(t, ErrUnavailable, err)
	})

	t.Run("SingleTarget", func(t *testing.T) {
		rec := &recordingTransport{}
		l := NewWeightedRoundRobinLoadBalancer(weightedTargets(rec, 0))
		driveLB(l, 5)
		assert.Equal(t, map[string]int{"t0": 5}, rec.counts())
	})

	t.Run("EqualWeightsIsPlainRoundRobin", func(t *testing.T) {
		rec := &recordingTransport{}
		l := NewWeightedRoundRobinLoadBalancer(weightedTargets(rec, 0, 0, 0)) // 0 -> 1
		driveLB(l, 6)
		assert.Equal(t, []string{"t0", "t1", "t2", "t0", "t1", "t2"}, rec.hits)
	})

	t.Run("DistributionAndSmoothness", func(t *testing.T) {
		rec := &recordingTransport{}
		l := NewWeightedRoundRobinLoadBalancer(weightedTargets(rec, 5, 1, 1))

		driveLB(l, 7) // one full SWRR cycle
		assert.Equal(t, []string{"t0", "t0", "t1", "t0", "t2", "t0", "t0"}, rec.hits,
			"heavy target is interleaved, not dealt in a burst")

		driveLB(l, 693) // 700 total = 100 cycles
		assert.Equal(t, map[string]int{"t0": 500, "t1": 100, "t2": 100}, rec.counts(),
			"exact long-run ratio 5:1:1")
	})

	t.Run("Concurrent", func(t *testing.T) {
		rec := &recordingTransport{}
		l := NewWeightedRoundRobinLoadBalancer(weightedTargets(rec, 3, 2, 1))
		var wg sync.WaitGroup
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				driveLB(l, 60)
			}()
		}
		wg.Wait()
		total := 0
		for _, c := range rec.counts() {
			total += c
		}
		assert.Equal(t, 3000, total, "every pick recorded; no lost/duplicated pick under -race")
	})
}
