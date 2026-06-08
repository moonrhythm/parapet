package purge

import (
	"testing"
	"time"

	"github.com/moonrhythm/parapet/pkg/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// putEntry seeds one entry into a cache.Storage with a known Meta.
func putEntry(t *testing.T, s cache.Storage, key string, m cache.Meta, body []byte) {
	t.Helper()
	m.Size = int64(len(body))
	w, err := s.Writer(key)
	require.NoError(t, err)
	_, err = w.Write(body)
	require.NoError(t, err)
	require.NoError(t, w.Commit(m))
}

func storageLen(s cache.Storage) int {
	n := 0
	s.Range(func(string, cache.Meta) bool { n++; return true })
	return n
}

func TestReap_DeletesInvalidatedKeepsOthers(t *testing.T) {
	s := cache.NewMemory(1 << 20)
	fresh := time.Now().Add(time.Hour).UnixNano()
	putEntry(t, s, "aa01aa01aa01aa01", cache.Meta{Host: "acme.com", URI: "/a", Created: 100, FreshUntil: fresh}, []byte("x"))
	putEntry(t, s, "bb02bb02bb02bb02", cache.Meta{Host: "other.com", URI: "/b", Created: 100, FreshUntil: fresh}, []byte("y"))
	putEntry(t, s, "cc03cc03cc03cc03", cache.Meta{Host: "acme.com", URI: "/c", Created: 250, FreshUntil: fresh}, []byte("z")) // created AFTER the purge

	tbl := New()
	fixedClock(tbl, 200)
	tbl.PurgeHost("acme.com") // host epoch 200

	assert.Equal(t, 1, tbl.Reap(s), "only the acme entry created <= 200 is reaped")

	_, _, okA := s.Get("aa01aa01aa01aa01")
	assert.False(t, okA, "acme.com /a (created 100 <= 200) reaped")
	_, _, okB := s.Get("bb02bb02bb02bb02")
	assert.True(t, okB, "other.com untouched (different host)")
	_, _, okC := s.Get("cc03cc03cc03cc03")
	assert.True(t, okC, "acme.com /c (created 250 > 200) survives — not over-reaped")

	// Reap does NOT retire records; the host record stays and keeps gating.
	assert.EqualValues(t, 1, tbl.Stats().HostRecs, "record kept (retirement intentionally not done)")
}

func TestReap_GlobalReapsAllIncludingEmptyHost(t *testing.T) {
	s := cache.NewMemory(1 << 20)
	fresh := time.Now().Add(time.Hour).UnixNano()
	putEntry(t, s, "aa01aa01aa01aa01", cache.Meta{Host: "", URI: "/old", Created: 100, FreshUntil: fresh}, []byte("x")) // no Host
	putEntry(t, s, "bb02bb02bb02bb02", cache.Meta{Host: "a.com", URI: "/y", Created: 100, FreshUntil: fresh}, []byte("y"))

	tbl := New()
	fixedClock(tbl, 200)
	tbl.FlushAll() // global epoch 200

	tbl.Reap(s)
	assert.Zero(t, storageLen(s), "flush-all reaps every entry, even one with an empty Host")
}

func TestReap_PrefixReaps(t *testing.T) {
	s := cache.NewMemory(1 << 20)
	fresh := time.Now().Add(time.Hour).UnixNano()
	putEntry(t, s, "aa01aa01aa01aa01", cache.Meta{Host: "acme.com", URI: "/blog/p1", Created: 100, FreshUntil: fresh}, []byte("x"))
	putEntry(t, s, "bb02bb02bb02bb02", cache.Meta{Host: "acme.com", URI: "/about", Created: 100, FreshUntil: fresh}, []byte("y"))

	tbl := New()
	fixedClock(tbl, 200)
	tbl.PurgePrefix("acme.com", "/blog")

	tbl.Reap(s)
	_, _, okA := s.Get("aa01aa01aa01aa01")
	assert.False(t, okA, "/blog/p1 reaped by the /blog prefix purge")
	_, _, okB := s.Get("bb02bb02bb02bb02")
	assert.True(t, okB, "/about untouched (outside the prefix)")
}

func TestReap_TagReapsAcrossHosts(t *testing.T) {
	s := cache.NewMemory(1 << 20)
	fresh := time.Now().Add(time.Hour).UnixNano()
	putEntry(t, s, "aa01aa01aa01aa01", cache.Meta{Host: "shop.com", URI: "/p1", Tags: []string{"product-42"}, Created: 100, FreshUntil: fresh}, []byte("x"))
	putEntry(t, s, "bb02bb02bb02bb02", cache.Meta{Host: "blog.com", URI: "/post", Tags: []string{"product-42"}, Created: 100, FreshUntil: fresh}, []byte("y"))
	putEntry(t, s, "cc03cc03cc03cc03", cache.Meta{Host: "shop.com", URI: "/p2", Tags: []string{"product-99"}, Created: 100, FreshUntil: fresh}, []byte("z"))

	tbl := New()
	fixedClock(tbl, 200)
	tbl.PurgeTag("product-42")

	assert.Equal(t, 2, tbl.Reap(s))
	_, _, okA := s.Get("aa01aa01aa01aa01")
	assert.False(t, okA, "tagged product-42 reaped (shop.com)")
	_, _, okB := s.Get("bb02bb02bb02bb02")
	assert.False(t, okB, "tagged product-42 reaped across hosts (blog.com)")
	_, _, okC := s.Get("cc03cc03cc03cc03")
	assert.True(t, okC, "product-99 untouched")
}

func TestReap_NoPurgesIsNoOp(t *testing.T) {
	s := cache.NewMemory(1 << 20)
	fresh := time.Now().Add(time.Hour).UnixNano()
	putEntry(t, s, "aa01aa01aa01aa01", cache.Meta{Host: "a.com", URI: "/a", Created: 100, FreshUntil: fresh}, []byte("x"))

	tbl := New()
	assert.Equal(t, 0, tbl.Reap(s), "nothing purged -> nothing reaped")
	assert.Equal(t, 1, storageLen(s))
}
