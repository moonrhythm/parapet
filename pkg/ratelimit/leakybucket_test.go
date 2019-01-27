package ratelimit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/ratelimit"
)

func TestLeakyBucket(t *testing.T) {
	t.Parallel()

	m := LeakyBucket(time.Second, 0)
	assert.IsType(t, &LeakyBucketStrategy{}, m.Strategy)
}
