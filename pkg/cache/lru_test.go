package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLRU_AdmitEvictsOverCap(t *testing.T) {
	l := newLRU(100)
	assert.Empty(t, l.admit("a", 40))
	assert.Empty(t, l.admit("b", 40))
	assert.EqualValues(t, 80, l.size())
	evicted := l.admit("c", 40) // 120 > 100 -> evict LRU "a"
	assert.Equal(t, []string{"a"}, evicted)
	assert.EqualValues(t, 80, l.size())
}

func TestLRU_TouchUpdatesRecency(t *testing.T) {
	l := newLRU(100)
	l.admit("a", 40)
	l.admit("b", 40)
	l.touch("a") // a most-recent; b becomes LRU
	assert.Equal(t, []string{"b"}, l.admit("c", 40))
}

func TestLRU_AdmitUpdatesSize(t *testing.T) {
	l := newLRU(100)
	l.admit("a", 40)
	l.admit("a", 60)
	assert.EqualValues(t, 60, l.size())
}

func TestLRU_Remove(t *testing.T) {
	l := newLRU(100)
	l.admit("a", 40)
	l.remove("a")
	assert.EqualValues(t, 0, l.size())
	l.remove("missing")
}
