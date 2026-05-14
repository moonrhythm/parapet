package pool

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSize(t *testing.T) {
	t.Parallel()

	assert.EqualValues(t, 16*1024, Size())
}

func TestGetReturnsBufferOfExpectedSize(t *testing.T) {
	t.Parallel()

	b := Get()
	if assert.NotNil(t, b) {
		assert.Len(t, *b, int(Size()))
	}
	Put(b)
}

func TestPutGetRoundTrip(t *testing.T) {
	t.Parallel()

	b := Get()
	(*b)[0] = 0xAB
	Put(b)

	// We can't assert pool reuse strictly (sync.Pool may evict), but Get must
	// still return a usable buffer of the right size.
	b2 := Get()
	defer Put(b2)
	assert.Len(t, *b2, int(Size()))
}
