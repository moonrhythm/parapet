package cache

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	storePut(t, s, "k1", Meta{Status: 200, Header: http.Header{}, FreshUntil: fresh, Size: 60}, make([]byte, 60))
	storePut(t, s, "k2", Meta{Status: 200, Header: http.Header{}, FreshUntil: fresh, Size: 60}, make([]byte, 60)) // 120 > 100 -> evict k1
	_, _, ok1 := s.Get("k1")
	_, _, ok2 := s.Get("k2")
	assert.False(t, ok1, "least-recently-used k1 evicted")
	assert.True(t, ok2)
	assert.EqualValues(t, 60, s.lru.size())
}

func TestMemory_GetSetDelete(t *testing.T) {
	s := NewMemory(1 << 20)
	storePut(t, s, "k", Meta{Status: 200, Header: http.Header{}, FreshUntil: time.Now().Add(time.Hour).UnixNano(), Size: 3}, []byte("abc"))
	m, body, ok := s.Get("k")
	assert.True(t, ok)
	assert.Equal(t, 200, m.Status)
	assert.Equal(t, "abc", string(body))
	s.Delete("k")
	_, _, ok = s.Get("k")
	assert.False(t, ok)
}

// The Meta returned by Get is a deep copy: mutating its Header/Vary must not touch
// the live stored entry that future hits serve.
func TestMemory_GetReturnsMetaCopy(t *testing.T) {
	s := NewMemory(1 << 20)
	fresh := time.Now().Add(time.Hour).UnixNano()
	storePut(t, s, "k", Meta{Status: 200, Header: http.Header{"X-Orig": {"1"}}, Vary: []string{"accept-encoding"}, FreshUntil: fresh, Size: 3}, []byte("abc"))

	m, _, ok := s.Get("k")
	require.True(t, ok)
	m.Header.Set("X-Injected", "pwned")
	m.Header.Set("X-Orig", "tampered")
	m.Vary[0] = "tampered"

	m2, _, ok := s.Get("k")
	require.True(t, ok)
	assert.Equal(t, "", m2.Header.Get("X-Injected"), "stored header must be unaffected by a caller's mutation")
	assert.Equal(t, "1", m2.Header.Get("X-Orig"))
	assert.Equal(t, []string{"accept-encoding"}, m2.Vary, "stored Vary must be unaffected")
}

// A Range fn that mutates the Meta it receives must not corrupt the live entry.
func TestMemory_RangeMetaMutationDoesNotCorrupt(t *testing.T) {
	s := NewMemory(1 << 20)
	fresh := time.Now().Add(time.Hour).UnixNano()
	storePut(t, s, "k", Meta{Status: 200, Header: http.Header{"X-Orig": {"1"}}, FreshUntil: fresh, Size: 3}, []byte("abc"))

	s.Range(func(_ string, m Meta) bool {
		m.Header.Set("X-Injected", "pwned") // a reaper that tags headers, say
		return true
	})

	m, _, ok := s.Get("k")
	require.True(t, ok)
	assert.Equal(t, "", m.Header.Get("X-Injected"), "Range fn mutation must not poison the cached entry")
}

func TestMemory_AbortAfterCommitIsNoop(t *testing.T) {
	s := NewMemory(1 << 20)
	w, err := s.Writer("k")
	require.NoError(t, err)
	_, err = w.Write([]byte("abc"))
	require.NoError(t, err)
	require.NoError(t, w.Commit(Meta{Status: 200, Header: http.Header{}, FreshUntil: time.Now().Add(time.Hour).UnixNano(), Size: 3}))
	w.Abort() // must be a no-op
	_, body, ok := s.Get("k")
	require.True(t, ok)
	assert.Equal(t, "abc", string(body))
}
