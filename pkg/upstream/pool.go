package upstream

import (
	"sync"
)

type _bytesPool struct {
	pool sync.Pool
}

func (p *_bytesPool) Get() []byte {
	return p.pool.Get().([]byte)
}

func (p *_bytesPool) Put(b []byte) {
	p.pool.Put(b)
}

var bytesPool = &_bytesPool{
	pool: sync.Pool{
		New: func() interface{} {
			return make([]byte, 32*1024)
		},
	},
}
