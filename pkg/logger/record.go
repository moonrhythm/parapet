package logger

import (
	"context"
	"sync"
	"time"
)

type ctxKeyRecord struct{}

type record struct {
	data    map[string]interface{}
	mu      sync.RWMutex
	disable bool
}

func newRecord() *record {
	return &record{data: make(map[string]interface{}, 18)}
}

func (r *record) Set(name string, value interface{}) {
	r.mu.Lock()
	r.data[name] = value
	r.mu.Unlock()
}

func (r *record) Get(name string) interface{} {
	r.mu.RLock()
	x := r.data[name]
	r.mu.RUnlock()
	return x
}

func (r *record) omitEmpty() {
	r.mu.Lock()
	for k, v := range r.data {
		if isEmpty(v) {
			delete(r.data, k)
		}
	}
	r.mu.Unlock()
}

func isEmpty(v interface{}) bool {
	switch v := v.(type) {
	case string:
		return v == ""
	case int:
		return v == 0
	case int64:
		return v == 0
	case int32:
		return v == 0
	case float64:
		return v == 0
	case float32:
		return v == 0
	case time.Time:
		return v.IsZero()
	case *time.Time:
		return v.IsZero()
	default:
		return false
	}
}

func getRecord(ctx context.Context) *record {
	d, _ := ctx.Value(ctxKeyRecord{}).(*record)
	return d
}

// Set sets log record field
func Set(ctx context.Context, name string, value interface{}) {
	r := getRecord(ctx)
	if r == nil {
		return
	}
	r.Set(name, value)
}

// Get gets log record field
func Get(ctx context.Context, name string) interface{} {
	r := getRecord(ctx)
	if r == nil {
		return nil
	}
	return r.Get(name)
}
