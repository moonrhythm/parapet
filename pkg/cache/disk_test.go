package cache

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	// 2s matches the repo's Eventually norm (1s was an outlier that an fsync- or
	// scheduler-stalled background scan could overrun under -race CI load). The
	// condition is monotonic, so a longer budget has zero false-pass risk. Kept
	// async on purpose: this is the only coverage that NewDisk launches the
	// startup scan goroutine at all.
	assert.Eventually(t, func() bool { return b.lru.size() > 0 }, 2*time.Second, 10*time.Millisecond,
		"startup scan re-seeds the LRU byte accounting")
}

func TestDisk_ScanReapsExpired(t *testing.T) {
	dir := t.TempDir()
	a, err := NewDisk(dir, 1<<20)
	require.NoError(t, err)
	const key = "aabbccddeeff00112233445566778899"
	storePut(t, a, key, Meta{Status: 200, Header: http.Header{}, FreshUntil: time.Now().Add(-time.Hour).UnixNano(), Size: 3}, []byte("xyz"))
	// Age the files so the age-gated reap treats them as not-in-flight.
	old := time.Now().Add(-2 * reapMinAge)
	require.NoError(t, os.Chtimes(a.metaPath(key), old, old))
	require.NoError(t, os.Chtimes(a.bodyPath(key), old, old))

	b := &DiskStorage{dir: dir, lru: newLRU(1 << 20)}
	b.scan(time.Now())
	_, _, ok := b.Get(key)
	assert.False(t, ok, "scan reaps the aged, expired entry")
}

func TestDisk_ScanSparesRecentlyWrittenExpired(t *testing.T) {
	dir := t.TempDir()
	a, err := NewDisk(dir, 1<<20)
	require.NoError(t, err)
	const key = "ffeeddccbbaa00112233445566778899"
	// Expired but just written (fresh mtime): a commit racing the startup scan.
	storePut(t, a, key, Meta{Status: 200, Header: http.Header{}, FreshUntil: time.Now().Add(-time.Hour).UnixNano(), Size: 3}, []byte("xyz"))

	b := &DiskStorage{dir: dir, lru: newLRU(1 << 20)}
	b.scan(time.Now())
	_, _, ok := b.Get(key)
	assert.True(t, ok, "a recently-written expired entry is spared (reaped on access instead)")
}

func TestDisk_PanicDuringFillNoTempLeak(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDisk(dir, 1<<20)
	require.NoError(t, err)
	c := New(d, Options{MaxFileSize: 1024})

	panicky := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	})
	mw := c.ServeHandler(panicky)
	func() {
		defer func() { _ = recover() }()
		mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://acme.com/boom", nil))
	}()

	ents, err := os.ReadDir(filepath.Join(dir, "tmp"))
	require.NoError(t, err)
	assert.Empty(t, ents, "panic during fill must abort the writer and leave no temp file")
}

func TestDisk_GetSetDelete(t *testing.T) {
	d, err := NewDisk(t.TempDir(), 1<<20)
	require.NoError(t, err)
	const key = "0011223344556677889900aabbccddee"
	storePut(t, d, key, Meta{Status: 200, Header: http.Header{}, FreshUntil: time.Now().Add(time.Hour).UnixNano(), Size: 3}, []byte("abc"))
	m, body, ok := d.Get(key)
	require.True(t, ok)
	assert.Equal(t, 200, m.Status)
	assert.Equal(t, "abc", string(body))
	d.Delete(key)
	_, _, ok = d.Get(key)
	assert.False(t, ok)
}

func TestDisk_RemoveFilesShortKeyNoPanic(t *testing.T) {
	d, err := NewDisk(t.TempDir(), 1<<20)
	require.NoError(t, err)
	assert.NotPanics(t, func() { d.removeFiles("x") })
	assert.NotPanics(t, func() { d.removeFiles("") })
}

func TestDisk_FsyncDir(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, fsyncDir(dir))
	assert.Error(t, fsyncDir(filepath.Join(dir, "does-not-exist")))
}

func TestDisk_ScanSkipsSizeMismatch(t *testing.T) {
	dir := t.TempDir()
	a, err := NewDisk(dir, 1<<20)
	require.NoError(t, err)
	const key = "1122334455667788990011223344aabb"
	storePut(t, a, key, Meta{Status: 200, Header: http.Header{}, FreshUntil: time.Now().Add(time.Hour).UnixNano(), Size: 3}, []byte("xyz"))
	require.NoError(t, os.WriteFile(a.bodyPath(key), []byte("xyzzz"), 0o644)) // body now 5, meta says 3

	b := &DiskStorage{dir: dir, lru: newLRU(1 << 20)}
	b.scan(time.Now())
	assert.EqualValues(t, 0, b.lru.size(), "size-mismatched entry is not admitted to the byte cap")
}
