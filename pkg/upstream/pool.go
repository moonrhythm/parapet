package upstream

import (
	"github.com/moonrhythm/parapet/pkg/internal/pool"
)

var bytesPool = &_bytesPool{}

type _bytesPool struct{}

func (p _bytesPool) Get() []byte {
	return *pool.Get()
}

func (p _bytesPool) Put(b []byte) {
	pool.Put(&b)
}
