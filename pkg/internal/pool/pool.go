package pool

import (
	"sync"
)

// Get gets bytes from pool
func Get() []byte {
	return bytesPool.Get().([]byte)
}

// Put puts bytes back to pool
func Put(b []byte) {
	bytesPool.Put(b)
}

// Size gets buffer size
func Size() int64 {
	return bufferSize
}

const bufferSize = 16 * 1024 // 16 KiB

var bytesPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, bufferSize)
	},
}
