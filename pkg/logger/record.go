package logger

import "context"

type ctxKeyRecord struct{}

type record struct {
	disable bool

	Date              string `json:"date"`
	Method            string `json:"method"`
	Host              string `json:"host"`
	URI               string `json:"uri"`
	UserAgent         string `json:"user_agent,omitempty"`
	Referer           string `json:"referer,omitempty"`
	RemoteIP          string `json:"remote_ip"`
	ForwardedFor      string `json:"forwarded_for,omitempty"`
	ForwardedProto    string `json:"forwarded_proto,omitempty"`
	Duration          int64  `json:"duration"`
	DurationHuman     string `json:"duration_human"`
	ContentLength     int64  `json:"content_length,omitempty"`
	StatusCode        int    `json:"status_code"`
	ResponseBodyBytes int64  `json:"response_body_bytes,omitempty"`
	RequestID         string `json:"request_id,omitempty"`
}

func getRecord(ctx context.Context) *record {
	d, _ := ctx.Value(ctxKeyRecord{}).(*record)
	return d
}
