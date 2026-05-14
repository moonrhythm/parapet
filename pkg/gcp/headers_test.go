package gcp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/moonrhythm/parapet/pkg/gcp"
)

func runHLB(t *testing.T, proxy int, xff string) string {
	t.Helper()

	m := HLBImmediateIP(proxy)
	var captured string
	h := m.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Get("X-Real-Ip")
	}))

	r := httptest.NewRequest("GET", "/", nil)
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	h.ServeHTTP(httptest.NewRecorder(), r)
	return captured
}

func TestHLBImmediateIP(t *testing.T) {
	t.Parallel()

	// XFF format from GCP HLB: client, ...hops, lb-ip
	// proxy=0 picks the second-to-last hop (the actual client as seen by GCP)
	assert.Equal(t, "203.0.113.1", runHLB(t, 0, "203.0.113.1, 35.191.0.1"))

	// with extra proxy hops
	assert.Equal(t, "203.0.113.1", runHLB(t, 1, "203.0.113.1, 10.0.0.1, 35.191.0.1"))

	// trims whitespace
	assert.Equal(t, "203.0.113.1", runHLB(t, 0, " 203.0.113.1 ,  35.191.0.1 "))
}

func TestHLBImmediateIPInsufficientHops(t *testing.T) {
	t.Parallel()

	// single entry, proxy=0 needs at least 2 -> do nothing
	assert.Empty(t, runHLB(t, 0, "203.0.113.1"))
}

func TestHLBImmediateIPNoHeader(t *testing.T) {
	t.Parallel()

	assert.Empty(t, runHLB(t, 0, ""))
}

func TestHLBImmediateIPNegativeProxy(t *testing.T) {
	t.Parallel()

	// negative is normalized to 0
	assert.Equal(t, "203.0.113.1", runHLB(t, -5, "203.0.113.1, 35.191.0.1"))
}
