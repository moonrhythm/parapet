package purge

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moonrhythm/parapet/pkg/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedClock pins the table's clock for deterministic epochs.
func fixedClock(t *Table, nanos int64) {
	t.now = func() time.Time { return time.Unix(0, nanos) }
}

// epochFor applies the lookup hook to a synthetic request for method/url.
func epochFor(t *Table, method, rawurl string) int64 {
	return t.InvalidatedAfter(httptest.NewRequest(method, rawurl, nil), cache.Meta{})
}

func TestTable_ScopesAndMax(t *testing.T) {
	tbl := New()
	fixedClock(tbl, 1000)

	// url scope: only the exact url is invalidated.
	tbl.PurgeURL("acme.com", "/a")
	assert.EqualValues(t, 1000, epochFor(tbl, "GET", "http://acme.com/a"))
	assert.EqualValues(t, 0, epochFor(tbl, "GET", "http://acme.com/b"), "sibling url untouched")

	// host scope: every url under the host. A later epoch wins via max.
	fixedClock(tbl, 2000)
	tbl.PurgeHost("acme.com")
	assert.EqualValues(t, 2000, epochFor(tbl, "GET", "http://acme.com/b"))
	assert.EqualValues(t, 2000, epochFor(tbl, "GET", "http://acme.com/a"), "host epoch > url epoch wins")
	assert.EqualValues(t, 0, epochFor(tbl, "GET", "http://other.com/a"), "other host untouched")

	// global scope: everything.
	fixedClock(tbl, 3000)
	tbl.FlushAll()
	assert.EqualValues(t, 3000, epochFor(tbl, "GET", "http://other.com/a"))
}

func TestTable_URLCoversMethodSchemeVariant(t *testing.T) {
	tbl := New()
	fixedClock(tbl, 7000)
	tbl.PurgeURL("acme.com", "/p?x=1")

	// Same host+uri, regardless of method/scheme, resolves to the same key.
	assert.EqualValues(t, 7000, epochFor(tbl, "GET", "http://acme.com/p?x=1"))
	assert.EqualValues(t, 7000, epochFor(tbl, "HEAD", "http://acme.com/p?x=1"))
	assert.EqualValues(t, 7000, epochFor(tbl, "GET", "https://acme.com/p?x=1"))
	// Different query is a different url.
	assert.EqualValues(t, 0, epochFor(tbl, "GET", "http://acme.com/p?x=2"))
}

func TestTable_HostNormalizationMatchesCacheKey(t *testing.T) {
	tbl := New()
	fixedClock(tbl, 5000)
	tbl.PurgeHost("ACME.com:443") // mixed case + port
	assert.EqualValues(t, 5000, epochFor(tbl, "GET", "http://acme.com/x"))
	assert.EqualValues(t, 5000, epochFor(tbl, "GET", "http://Acme.COM:8443/x"))
}

func TestTable_MonotonicClampOnClockStepBack(t *testing.T) {
	tbl := New()
	fixedClock(tbl, 10_000)
	tbl.PurgeHost("a.com")
	assert.EqualValues(t, 10_000, epochFor(tbl, "GET", "http://a.com/"))

	// Clock steps back; a new purge must not get a lower epoch.
	fixedClock(tbl, 9_000)
	tbl.PurgeHost("b.com")
	assert.EqualValues(t, 10_000, epochFor(tbl, "GET", "http://b.com/"), "clamped to highWater")

	// A flush after a step-back is also clamped.
	fixedClock(tbl, 8_000)
	tbl.FlushAll()
	assert.GreaterOrEqual(t, epochFor(tbl, "GET", "http://c.com/"), int64(10_000))
}

func TestTable_FlushAllClearsAndSupersedes(t *testing.T) {
	tbl := New()
	fixedClock(tbl, 1000)
	tbl.PurgeURL("a.com", "/x")
	tbl.PurgeHost("b.com")
	tbl.PurgePrefix("c.com", "/blog")
	tbl.PurgeTag("t1")

	fixedClock(tbl, 5000)
	tbl.FlushAll()

	st := tbl.Stats()
	assert.Zero(t, st.HostRecs, "host map cleared on flush")
	assert.Zero(t, st.URLRecs, "url map cleared on flush")
	assert.Zero(t, st.PrefixRecs, "prefix map cleared on flush")
	assert.Zero(t, st.TagRecs, "tag map cleared on flush")
	assert.EqualValues(t, 5000, epochFor(tbl, "GET", "http://anything.com/q"))
}

func TestTable_CapFoldBoundsMemory(t *testing.T) {
	tbl := New(WithMaxRecords(2)) // tiny cap to force a fold
	fixedClock(tbl, 1000)
	tbl.PurgeURL("a.com", "/1")
	tbl.PurgeURL("a.com", "/2")
	tbl.PurgeURL("a.com", "/3") // overflow -> fold

	st := tbl.Stats()
	assert.Zero(t, st.URLRecs, "overflowing url map folded to global")
	assert.EqualValues(t, 1, st.Folds)
	// Conservative: the folded urls are still invalidated (via the global epoch).
	assert.EqualValues(t, 1000, epochFor(tbl, "GET", "http://a.com/1"))
	assert.EqualValues(t, 1000, epochFor(tbl, "GET", "http://a.com/2"))
}

func TestTable_InvalidatedAfterMetaMatchesRequest(t *testing.T) {
	tbl := New()
	fixedClock(tbl, 4000)
	tbl.PurgeURL("acme.com", "/p?x=1")

	// The reaper's Meta-based lookup agrees with the request-based hook.
	m := cache.Meta{Host: "acme.com", URI: "/p?x=1"}
	assert.EqualValues(t, 4000, tbl.InvalidatedAfterMeta(m))
	assert.Equal(t, epochFor(tbl, "GET", "http://acme.com/p?x=1"), tbl.InvalidatedAfterMeta(m))
	assert.EqualValues(t, 0, tbl.InvalidatedAfterMeta(cache.Meta{Host: "acme.com", URI: "/p?x=2"}))
	// An empty-Host (old) entry matches only the global scope.
	assert.EqualValues(t, 0, tbl.InvalidatedAfterMeta(cache.Meta{Host: "", URI: "/p?x=1"}))
}

func TestTable_PrefixScope(t *testing.T) {
	tbl := New()
	fixedClock(tbl, 1000)
	tbl.PurgePrefix("acme.com", "/blog")

	assert.EqualValues(t, 1000, epochFor(tbl, "GET", "http://acme.com/blog"))
	assert.EqualValues(t, 1000, epochFor(tbl, "GET", "http://acme.com/blog/post-1"))
	assert.EqualValues(t, 1000, epochFor(tbl, "GET", "http://acme.com/blog/post-1?utm=x"))
	assert.EqualValues(t, 0, epochFor(tbl, "GET", "http://acme.com/blogger"), "path boundary respected")
	assert.EqualValues(t, 0, epochFor(tbl, "GET", "http://acme.com/about"))
	assert.EqualValues(t, 0, epochFor(tbl, "GET", "http://other.com/blog/x"), "prefix is host-scoped")
}

func TestTable_PrefixNormalizationAndRoot(t *testing.T) {
	tbl := New()
	fixedClock(tbl, 2000)
	tbl.PurgePrefix("a.com", "/docs/") // trailing slash normalized away
	assert.EqualValues(t, 2000, epochFor(tbl, "GET", "http://a.com/docs"))
	assert.EqualValues(t, 2000, epochFor(tbl, "GET", "http://a.com/docs/intro"))
	assert.EqualValues(t, 0, epochFor(tbl, "GET", "http://a.com/docsource"))

	fixedClock(tbl, 3000)
	tbl.PurgePrefix("b.com", "/") // whole-host prefix
	assert.EqualValues(t, 3000, epochFor(tbl, "GET", "http://b.com/anything/here"))
}

func TestTable_PrefixRepeatUpdatesEpochInPlace(t *testing.T) {
	tbl := New()
	fixedClock(tbl, 1000)
	tbl.PurgePrefix("a.com", "/x")
	fixedClock(tbl, 5000)
	tbl.PurgePrefix("a.com", "/x")
	assert.EqualValues(t, 1, tbl.Stats().PrefixRecs, "same prefix updates in place")
	assert.EqualValues(t, 5000, epochFor(tbl, "GET", "http://a.com/x/y"), "epoch advanced")
}

func TestTable_PrefixCapFold(t *testing.T) {
	tbl := New(WithMaxRecords(2))
	fixedClock(tbl, 1000)
	tbl.PurgePrefix("a.com", "/1")
	tbl.PurgePrefix("a.com", "/2")
	tbl.PurgePrefix("a.com", "/3") // overflow -> fold to global
	assert.Zero(t, tbl.Stats().PrefixRecs, "overflowing prefix records folded into global")
	assert.EqualValues(t, 1, tbl.Stats().Folds)
	assert.EqualValues(t, 1000, epochFor(tbl, "GET", "http://a.com/1"), "still invalidated via global")
}

func TestTable_NoPurgeGate(t *testing.T) {
	tbl := New()
	// Before any purge: the serving-path gate short-circuits to 0 without locking.
	assert.False(t, tbl.active.Load())
	assert.EqualValues(t, 0, tbl.InvalidatedAfterMeta(cache.Meta{Host: "a.com", URI: "/x", Tags: []string{"t"}}))

	fixedClock(tbl, 1000)
	tbl.PurgeHost("a.com")
	assert.True(t, tbl.active.Load())
	assert.EqualValues(t, 1000, tbl.InvalidatedAfterMeta(cache.Meta{Host: "a.com", URI: "/x"}))
	assert.EqualValues(t, 0, tbl.InvalidatedAfterMeta(cache.Meta{Host: "other.com", URI: "/x"}))
}

func TestTable_TagScope(t *testing.T) {
	tbl := New()
	fixedClock(tbl, 1000)
	tbl.PurgeTag("product-42")

	assert.EqualValues(t, 1000, tbl.InvalidatedAfterMeta(cache.Meta{Host: "shop.com", URI: "/p", Tags: []string{"product-42"}}))
	assert.EqualValues(t, 1000, tbl.InvalidatedAfterMeta(cache.Meta{Host: "other.com", URI: "/x", Tags: []string{"a", "product-42"}}))
	assert.EqualValues(t, 0, tbl.InvalidatedAfterMeta(cache.Meta{Host: "shop.com", URI: "/p", Tags: []string{"category-shoes"}}))
	assert.EqualValues(t, 0, tbl.InvalidatedAfterMeta(cache.Meta{Host: "shop.com", URI: "/p"}))
}

func TestTable_SnapshotRestoreRoundTrip(t *testing.T) {
	src := New()
	fixedClock(src, 4000)
	src.PurgeHost("a.com")
	src.PurgeURL("b.com", "/p")
	src.PurgePrefix("c.com", "/sec")
	src.PurgeTag("sku-7")

	// Round-trip through JSON to exercise the serialized shape.
	data, err := json.Marshal(src.Snapshot())
	require.NoError(t, err)
	var snap Snapshot
	require.NoError(t, json.Unmarshal(data, &snap))

	dst := New()
	dst.Restore(snap)
	assert.True(t, dst.active.Load(), "restore re-opens the gate")
	assert.EqualValues(t, 4000, epochFor(dst, "GET", "http://a.com/x"))
	assert.EqualValues(t, 4000, epochFor(dst, "GET", "http://b.com/p"))
	assert.EqualValues(t, 4000, epochFor(dst, "GET", "http://c.com/sec/page"))
	assert.EqualValues(t, 4000, dst.InvalidatedAfterMeta(cache.Meta{Tags: []string{"sku-7"}}))

	// highWater restored: a purge under a stepped-back clock still clamps up.
	fixedClock(dst, 1)
	dst.PurgeHost("d.com")
	assert.EqualValues(t, 4000, epochFor(dst, "GET", "http://d.com/"), "highWater reloaded")
}

func TestTable_SnapshotRestoreGlobalFlush(t *testing.T) {
	src := New()
	fixedClock(src, 9000)
	src.FlushAll()

	dst := New()
	dst.Restore(src.Snapshot())
	assert.EqualValues(t, 9000, epochFor(dst, "GET", "http://anything.com/q"), "global flush survives restore")
}
