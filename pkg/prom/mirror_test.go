package prom_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moonrhythm/parapet/pkg/mirror"

	. "github.com/moonrhythm/parapet/pkg/prom"
)

func TestMirror(t *testing.T) {
	observe := Mirror()
	require.NotNil(t, observe)

	// Baselines so the test is order- and -count-independent against the shared
	// process-global registry (the counters persist across tests/runs).
	outcomes := []string{"dispatched", "completed", "dropped_full", "dropped_oversize", "panicked"}
	base := map[string]float64{}
	for _, o := range outcomes {
		v := counterValue(t, "parapet_mirror_total", map[string]string{"outcome": o})
		if v < 0 {
			v = 0 // series not created yet (counterValue returns -1 for absent)
		}
		base[o] = v
	}
	baseDuration := histogramCount(t, "parapet_mirror_request_duration_seconds", nil)

	observe(mirror.MirrorInfo{Outcome: mirror.OutcomeDispatched})
	observe(mirror.MirrorInfo{Outcome: mirror.OutcomeCompleted, Status: 200, Duration: 5 * time.Millisecond})
	observe(mirror.MirrorInfo{Outcome: mirror.OutcomeDroppedFull})
	observe(mirror.MirrorInfo{Outcome: mirror.OutcomeDroppedOversize})
	observe(mirror.MirrorInfo{Outcome: mirror.OutcomePanicked})

	for _, o := range outcomes {
		assert.EqualValues(t, base[o]+1, counterValue(t, "parapet_mirror_total", map[string]string{"outcome": o}),
			"outcome %q counted once", o)
	}
	assert.EqualValues(t, baseDuration+1, histogramCount(t, "parapet_mirror_request_duration_seconds", nil),
		"only a completed round-trip contributes a duration sample")
}

func ExampleMirror() {
	mr := mirror.New()
	mr.Observe = Mirror() // prom.Mirror(): count by outcome, observe completed-round-trip latency
	_ = mr                // mr.Use(canary); s.Use(mr)
}
