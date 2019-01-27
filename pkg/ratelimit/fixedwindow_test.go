package ratelimit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/ratelimit"
)

func TestFixedWindow(t *testing.T) {
	t.Parallel()

	m := FixedWindow(2, time.Second)
	assert.IsType(t, &FixedWindowStrategy{}, m.Strategy)
}

func TestFixedWindowPerSecond(t *testing.T) {
	t.Parallel()

	m := FixedWindowPerSecond(2)
	assert.IsType(t, &FixedWindowStrategy{}, m.Strategy)
}

func TestFixedWindowPerMinute(t *testing.T) {
	t.Parallel()

	m := FixedWindowPerMinute(2)
	assert.IsType(t, &FixedWindowStrategy{}, m.Strategy)
}

func TestFixedWindowPerHour(t *testing.T) {
	t.Parallel()

	m := FixedWindowPerHour(2)
	assert.IsType(t, &FixedWindowStrategy{}, m.Strategy)
}
