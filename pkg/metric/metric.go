package metric

import (
	"sync"
)

var (
	state = make(map[string]interface{})
	mu    sync.RWMutex
)

// Set sets metric state
func Set(key string, value interface{}) {
	mu.Lock()
	state[key] = value
	mu.Unlock()
}

// Incr inceases state by delta
func Incr(key string, delta int64) {
	mu.Lock()
	curr, _ := state[key].(int64)
	state[key] = curr + delta
	mu.Unlock()
}

// Get gets current state
func Get() map[string]interface{} {
	mu.RLock()
	m := make(map[string]interface{})
	for k, v := range state {
		m[k] = v
	}
	mu.RUnlock()
	return m
}
