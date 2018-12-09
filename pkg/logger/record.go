package logger

import (
	"context"
	"time"
)

type ctxKeyRecord struct{}

type record struct {
	disable bool
	data    map[string]interface{}
}

func newRecord() *record {
	return &record{data: make(map[string]interface{})}
}

func (r *record) Set(name string, value interface{}) {
	r.data[name] = value
}

func (r *record) omitEmpty() {
	for k, v := range r.data {
		if isEmpty(v) {
			delete(r.data, k)
		}
	}
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
