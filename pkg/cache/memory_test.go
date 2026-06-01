package cache

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMemory_DoesNotPersistAcrossInstances(t *testing.T) {
	var calls int32
	h := origin(originSpec{body: []byte("m"), header: hdr("Cache-Control", "max-age=60")}, &calls)

	ca := New(NewMemory(1<<20), Options{MaxFileSize: 1024})
	do(ca, h, "GET", "http://acme.com/m", nil)
	assert.Equal(t, "HIT", do(ca, h, "GET", "http://acme.com/m", nil).Header().Get("X-Cache"))

	// A fresh memory storage shares nothing (no persistence).
	cb := New(NewMemory(1<<20), Options{MaxFileSize: 1024})
	assert.Equal(t, "MISS", do(cb, h, "GET", "http://acme.com/m", nil).Header().Get("X-Cache"))
}

func TestMemory_LRUEvicts(t *testing.T) {
	s := NewMemory(100)
	fresh := time.Now().Add(time.Hour).UnixNano()
	s.Set("k1", Meta{Status: 200, Header: http.Header{}, FreshUntil: fresh, Size: 60}, make([]byte, 60))
	s.Set("k2", Meta{Status: 200, Header: http.Header{}, FreshUntil: fresh, Size: 60}, make([]byte, 60)) // 120 > 100 -> evict k1
	_, _, ok1 := s.Get("k1")
	_, _, ok2 := s.Get("k2")
	assert.False(t, ok1, "least-recently-used k1 evicted")
	assert.True(t, ok2)
	assert.EqualValues(t, 60, s.lru.size())
}

func TestMemory_GetSetDelete(t *testing.T) {
	s := NewMemory(1 << 20)
	s.Set("k", Meta{Status: 200, Header: http.Header{}, FreshUntil: time.Now().Add(time.Hour).UnixNano(), Size: 3}, []byte("abc"))
	m, body, ok := s.Get("k")
	assert.True(t, ok)
	assert.Equal(t, 200, m.Status)
	assert.Equal(t, "abc", string(body))
	s.Delete("k")
	_, _, ok = s.Get("k")
	assert.False(t, ok)
}
