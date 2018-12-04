package logger

import "context"

type ctxKeyRecord struct{}

type record struct {
	disable bool
	data    map[string]interface{}
}

func newRecord() *record {
	return &record{data: make(map[string]interface{})}
}

func (r *record) Set(name string, value interface{}) {
	if value == "" || value == 0 {
		return
	}
	r.data[name] = value
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
