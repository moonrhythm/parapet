package ratelimit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/ratelimit"
)

func TestConcurrentQueue(t *testing.T) {
	t.Parallel()

	m := ConcurrentQueue(0, 0)
	assert.IsType(t, &ConcurrentQueueStrategy{}, m.Strategy)
}
