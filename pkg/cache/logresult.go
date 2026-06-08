package cache

import (
	"net/http"

	"github.com/moonrhythm/parapet/pkg/logger"
)

// LogResult is a ResultFunc that records the cache outcome on the request's
// structured-logger record as the field "cacheStatus" (HIT, MISS, STALE,
// STALE_ERROR, or BYPASS), so it appears in access logs alongside the upstream,
// status, and timing fields. It is a no-op when no logger middleware is mounted
// ahead of the cache (logger.Set ignores a request with no record).
//
// Wire it via Options.OnResult, alone or composed with prom.Cache:
//
//	cache.New(store, cache.Options{OnResult: cache.LogResult})
func LogResult(r *http.Request, info ResultInfo) {
	logger.Set(r.Context(), "cacheStatus", string(info.Result))
}
