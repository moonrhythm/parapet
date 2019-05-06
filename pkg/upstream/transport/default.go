package transport

import (
	"time"
)

const (
	defaultDialTimeout           = 5 * time.Second
	defaultMaxIdleConns          = 32
	defaultTCPKeepAlive          = time.Minute
	defaultIdleConnTimeout       = 10 * time.Minute
	defaultResponseHeaderTimeout = time.Minute
	defaultExpectContinueTimeout = time.Second
	defaultTLSHandshakeTimeout   = 5 * time.Second
)
