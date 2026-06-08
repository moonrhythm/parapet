package cache

import (
	"net/http"
	"time"
)

// Result is the cache outcome for one request, reported to Options.OnResult. Its
// string value matches the X-Cache response header where one is sent (HIT, MISS,
// STALE) and additionally names the two states X-Cache cannot distinguish: a
// stale-if-error fallback (vs a stale-while-revalidate serve, both "STALE" on the
// wire) and a bypass (which sends no X-Cache header at all).
type Result string

const (
	// ResultHit: served from a fresh stored entry (X-Cache: HIT).
	ResultHit Result = "HIT"

	// ResultMiss: not served from a stored entry; the origin was contacted and, if
	// the response was cacheable, stored (X-Cache: MISS).
	ResultMiss Result = "MISS"

	// ResultStale: served a stale stored entry under RFC 5861
	// stale-while-revalidate and refreshed it in the background (X-Cache: STALE).
	ResultStale Result = "STALE"

	// ResultStaleError: served a stale stored entry under RFC 5861 stale-if-error
	// because revalidating with the origin failed (its X-Cache is also "STALE").
	ResultStaleError Result = "STALE_ERROR"

	// ResultBypass: the request was ineligible for caching — a non-cacheable method,
	// a protocol upgrade, a Range request, or Options.Cacheable returned false — and
	// was proxied straight to the origin. This path sends no X-Cache header, so the
	// hook is the only way to observe it.
	ResultBypass Result = "BYPASS"
)

// ResultInfo carries the details of one request's cache outcome to a ResultFunc.
type ResultInfo struct {
	// Result is the cache decision (HIT, MISS, STALE, STALE_ERROR, BYPASS).
	Result Result

	// FillDuration is how long the foreground origin fetch took, set only when this
	// request actually contacted the origin on the serving path: a MISS fill and a
	// stale-if-error revalidation attempt. It is zero for a HIT, for a
	// stale-while-revalidate STALE (served from cache; the background refresh runs
	// detached and is never reported), and for a BYPASS.
	FillDuration time.Duration
}

// ResultFunc observes a cache outcome. Assign one to Options.OnResult to make the
// cache observable — see prom.Cache for Prometheus metrics and cache.LogResult for
// a structured-log field. It is invoked once per request the cache serves,
// synchronously on the foreground serving path, and never from the background
// stale-while-revalidate goroutine (which has no client request to attribute and
// must not touch request-scoped state such as the logger record). A panic
// propagating out of the origin handler during a fill is not reported (it unwinds
// past the hook); every request that returns normally is.
type ResultFunc func(r *http.Request, info ResultInfo)
