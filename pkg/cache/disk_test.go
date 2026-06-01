package cache

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisk_RestartPersistence(t *testing.T) {
	dir := t.TempDir()

	a, err := NewDisk(dir, 1<<20)
	require.NoError(t, err)
	ca := New(a, Options{MaxFileSize: 1024})
	var callsA int32
	ha := origin(originSpec{body: []byte("persist"), header: hdr("Cache-Control", "max-age=600")}, &callsA)
	do(ca, ha, "GET", "http://acme.com/p", nil) // store on disk

	// A fresh storage over the same dir serves the entry from disk.
	b, err := NewDisk(dir, 1<<20)
	require.NoError(t, err)
	cb := New(b, Options{MaxFileSize: 1024})
	var callsB int32
	hb := origin(originSpec{body: []byte("persist"), header: hdr("Cache-Control", "max-age=600")}, &callsB)
	r := do(cb, hb, "GET", "http://acme.com/p", nil)
	assert.Equal(t, "HIT", r.Header().Get("X-Cache"), "survived restart")
	assert.EqualValues(t, 0, atomic.LoadInt32(&callsB), "served from disk, origin not contacted")

	assert.Eventually(t, func() bool { return b.lru.size() > 0 }, time.Second, 10*time.Millisecond,
		"startup scan re-seeds the LRU byte accounting")
}

func TestDisk_ScanReapsExpired(t *testing.T) {
	dir := t.TempDir()
	a, err := NewDisk(dir, 1<<20)
	require.NoError(t, err)
	const key = "aabbccddeeff00112233445566778899"
	a.Set(key, Meta{
		Status:     200,
		Header:     http.Header{},
		FreshUntil: time.Now().Add(-time.Hour).UnixNano(), // already expired
		Size:       3,
	}, []byte("xyz"))

	// A new storage scans on startup and reaps the expired entry.
	b, err := NewDisk(dir, 1<<20)
	require.NoError(t, err)
	assert.Eventually(t, func() bool {
		_, _, ok := b.Get(key)
		return !ok
	}, time.Second, 10*time.Millisecond, "scan reaps the expired entry")
}

func TestDisk_GetSetDelete(t *testing.T) {
	d, err := NewDisk(t.TempDir(), 1<<20)
	require.NoError(t, err)
	const key = "0011223344556677889900aabbccddee"
	d.Set(key, Meta{Status: 200, Header: http.Header{}, FreshUntil: time.Now().Add(time.Hour).UnixNano(), Size: 3}, []byte("abc"))
	m, body, ok := d.Get(key)
	require.True(t, ok)
	assert.Equal(t, 200, m.Status)
	assert.Equal(t, "abc", string(body))
	d.Delete(key)
	_, _, ok = d.Get(key)
	assert.False(t, ok)
}
