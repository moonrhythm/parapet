package cache

import (
	"bytes"
	"net/http"
	"sort"
	"strings"
	"time"
)

// hopByHop headers are connection-specific and must not be stored in (or served
// from) the shared cache.
var hopByHop = map[string]struct{}{
	"Connection":          {},
	"Proxy-Connection":    {},
	"Keep-Alive":          {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
}

// teeWriter wraps the client ResponseWriter on a cache fill (the leader path). It
// streams the response to the client AND, when cacheable, to a storage
// EntryWriter (the disk backend streams to a temp file; memory buffers). On
// finish it Commits iff the body is complete (Content-Length matched, or HEAD),
// guaranteeing a truncated response is never stored; otherwise it Aborts. cleanup
// Aborts on a panic before finish so no temp/partial state is left.
type teeWriter struct {
	freshUntil time.Time
	rw         http.ResponseWriter
	ew         EntryWriter // nil = not caching this response
	r          *http.Request
	c          *Cache
	lock       *fillLock // the leader's fill lock; its waiter count gates DecoupleFill
	metaHeader http.Header
	method     string
	storeKey   string
	primaryHex string
	vary       []string     // sorted, lowercased
	tags       []string     // surrogate keys parsed from the response Cache-Tag header
	leaderBuf  bytes.Buffer // DecoupleFill: the leader's own copy of the body, served after the lock is released
	written    int64
	contentLen int64
	status     int

	wroteHeader    bool
	hasCL          bool
	deferredClient bool // DecoupleFill: body buffered for the leader; the client is served later
}

func (tw *teeWriter) Header() http.Header { return tw.rw.Header() }

func (tw *teeWriter) WriteHeader(code int) {
	if tw.wroteHeader {
		return
	}
	tw.wroteHeader = true
	tw.status = code

	h := tw.rw.Header()
	reqAuthorized := tw.r.Header.Get("Authorization") != ""
	dec := decide(tw.method, code, h, reqAuthorized, tw.c.maxFileSize, time.Now())
	if dec.cacheable {
		vary := append([]string(nil), dec.vary...)
		sort.Strings(vary)
		// Store under the key derived from THIS response's Vary + the request's
		// values, so a later lookup matches once the Vary map is learned.
		storeKey := variantHashFor(tw.primaryHex, vary, tw.r.Header)
		if ew, err := tw.c.storage.Writer(storeKey); err == nil {
			tw.ew = ew
			tw.storeKey = storeKey
			tw.vary = vary
			tw.tags = parseCacheTags(h)
			tw.freshUntil = dec.freshUntil
			tw.metaHeader = sanitizeHeader(h)
			if cl, ok := contentLength(h); ok {
				tw.hasCL = true
				tw.contentLen = cl
			}
		}
	}
	// DecoupleFill, but only when the fill is actually CONTENDED — at least one
	// follower is already blocked on this lock. Then buffer the body for the leader
	// and stream it to storage, serving the client later (see fill/serveLeader) so a
	// slow client can't hold the lock from those followers. With no follower waiting
	// there's nothing to isolate, so the leader streams in lockstep and pays no
	// added latency. A non-cacheable response (ew == nil) always streams in lockstep.
	if tw.ew != nil && tw.c.decoupleFill && tw.lock != nil && tw.lock.waiters.Load() > 0 {
		tw.deferredClient = true
		return
	}
	h.Set("X-Cache", "MISS")
	tw.rw.WriteHeader(code)
}

func (tw *teeWriter) Write(p []byte) (int, error) {
	if !tw.wroteHeader {
		tw.WriteHeader(http.StatusOK)
	}
	// DecoupleFill: don't touch (or block on) the client now — keep the leader's own
	// copy of the body (capped at maxFileSize) and, independently, stream to storage.
	// serveLeader writes the buffered copy to the client after the lock is released,
	// so the leader always gets exactly what the origin produced regardless of whether
	// caching succeeds (an oversize/error/truncated fill just isn't cached). Report
	// success to the origin handler.
	if tw.deferredClient {
		if room := tw.c.maxFileSize - int64(tw.leaderBuf.Len()); room > 0 {
			if int64(len(p)) <= room {
				tw.leaderBuf.Write(p)
			} else {
				tw.leaderBuf.Write(p[:room])
			}
		}
		if tw.ew != nil {
			switch {
			case tw.written+int64(len(p)) > tw.c.maxFileSize:
				tw.abort()
			default:
				if _, werr := tw.ew.Write(p); werr != nil {
					tw.abort()
				} else {
					tw.written += int64(len(p))
				}
			}
		}
		return len(p), nil
	}
	n, err := tw.rw.Write(p)
	if tw.ew != nil {
		switch {
		case tw.written+int64(len(p)) > tw.c.maxFileSize:
			tw.abort() // oversize -> stop caching; client still gets the full response
		default:
			if _, werr := tw.ew.Write(p); werr != nil {
				tw.abort()
			} else {
				tw.written += int64(len(p))
			}
		}
	}
	return n, err
}

// abort stops caching this response and discards the in-progress entry.
func (tw *teeWriter) abort() {
	if tw.ew != nil {
		tw.ew.Abort()
		tw.ew = nil
	}
}

// finish commits the entry iff the body is complete; a truncated body (written !=
// Content-Length) or an abort discards it. Runs after the upstream handler
// returns (the client already has the full response) and before the caller
// closes the fill lock, so waiting followers find the committed entry.
func (tw *teeWriter) finish() {
	if tw.ew == nil {
		return
	}
	complete := tw.method == http.MethodHead || (tw.hasCL && tw.written == tw.contentLen)
	if !complete {
		tw.abort()
		return
	}
	meta := Meta{
		Status:     tw.status,
		Header:     tw.metaHeader,
		PrimaryHex: tw.primaryHex,
		Host:       normalizeHost(tw.r.Host),
		URI:        tw.r.URL.RequestURI(),
		Vary:       tw.vary,
		Tags:       tw.tags,
		Created:    time.Now().UnixNano(),
		FreshUntil: tw.freshUntil.UnixNano(),
		Size:       tw.written,
	}
	if err := tw.ew.Commit(meta); err == nil {
		tw.c.setPrimaryVary(tw.primaryHex, tw.vary)
	}
	tw.ew = nil
}

// serveLeader writes the response to the leader's client AFTER the fill lock has
// been released (DecoupleFill). It serves the body the leader buffered during the
// fill — never reading back from storage — so it is unaffected by whether the entry
// was committed, evicted, deleted, or purged in the meantime. It serves the
// sanitized response headers (hop-by-hop stripped, like a stored entry) tagged MISS,
// since this request contacted the origin. A truncated/over-cap fill simply yields
// the bytes the origin actually wrote (matching the lockstep path); HEAD and bodiless
// statuses carry no body.
func (tw *teeWriter) serveLeader() {
	h := tw.rw.Header()
	for k := range h { // drop the origin's raw (unsanitized) headers
		delete(h, k)
	}
	for k, vs := range tw.metaHeader {
		h[k] = append([]string(nil), vs...)
	}
	h.Set("X-Cache", "MISS")
	tw.rw.WriteHeader(tw.status)
	if tw.method != http.MethodHead && tw.status != http.StatusNoContent {
		_, _ = tw.rw.Write(tw.leaderBuf.Bytes())
	}
}

// cleanup aborts an uncommitted entry. Deferred on the leader path so a panic in
// the upstream handler (before finish) doesn't leak the temp file.
func (tw *teeWriter) cleanup() {
	if tw.ew != nil {
		tw.ew.Abort()
		tw.ew = nil
	}
}

// Flush forwards to the underlying writer so streaming responses still flush. In
// DecoupleFill mode it is a no-op while buffering: the client hasn't been written
// to yet, so flushing it would prematurely commit a 200 and defeat the deferral
// (the whole response is delivered at once from storage by serveLeader).
func (tw *teeWriter) Flush() {
	if tw.deferredClient {
		return
	}
	if f, ok := tw.rw.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController (Flush/Hijack
// etc. via the standard mechanism).
func (tw *teeWriter) Unwrap() http.ResponseWriter { return tw.rw }

// sanitizeHeader clones h for the stored meta, dropping hop-by-hop headers (the
// X-Cache tag is not yet set when this is called).
func sanitizeHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vs := range h {
		if _, hop := hopByHop[http.CanonicalHeaderKey(k)]; hop {
			continue
		}
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// Surrogate-key (Cache-Tag) caps: an entry's tags are stored in its Meta and held
// in RAM by the memory backend, so bound both the count and the length per entry so
// a hostile/buggy origin can't bloat metadata.
const (
	maxCacheTags   = 64
	maxCacheTagLen = 256
)

// parseCacheTags extracts surrogate keys from the response Cache-Tag header(s):
// comma-separated, trimmed, de-duplicated, and capped (count + per-tag length).
// Returns nil when there are none, so a no-tag response stores no Tags. The header
// is left on the response (capture-only) — strip it at the origin if it must not
// reach clients.
func parseCacheTags(h http.Header) []string {
	values := h.Values("Cache-Tag")
	if len(values) == 0 {
		return nil
	}
	var tags []string
	seen := make(map[string]struct{})
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if t == "" || len(t) > maxCacheTagLen {
				continue
			}
			if _, dup := seen[t]; dup {
				continue
			}
			seen[t] = struct{}{}
			tags = append(tags, t)
			if len(tags) >= maxCacheTags {
				return tags
			}
		}
	}
	return tags
}
