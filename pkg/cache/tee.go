package cache

import (
	"net/http"
	"sort"
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
//
//nolint:govet
type teeWriter struct {
	rw         http.ResponseWriter
	r          *http.Request
	c          *Cache
	method     string
	primaryHex string

	wroteHeader bool
	status      int

	ew         EntryWriter // nil = not caching this response
	written    int64
	contentLen int64
	hasCL      bool
	storeKey   string
	freshUntil time.Time
	vary       []string // sorted, lowercased
	metaHeader http.Header
}

func (tw *teeWriter) Header() http.Header { return tw.rw.Header() }

func (tw *teeWriter) WriteHeader(code int) {
	if tw.wroteHeader {
		return
	}
	tw.wroteHeader = true
	tw.status = code

	h := tw.rw.Header()
	dec := decide(tw.method, code, h, tw.c.maxFileSize, time.Now())
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
			tw.freshUntil = dec.freshUntil
			tw.metaHeader = sanitizeHeader(h)
			if cl, ok := contentLength(h); ok {
				tw.hasCL = true
				tw.contentLen = cl
			}
		}
	}
	h.Set("X-Cache", "MISS")
	tw.rw.WriteHeader(code)
}

func (tw *teeWriter) Write(p []byte) (int, error) {
	if !tw.wroteHeader {
		tw.WriteHeader(http.StatusOK)
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
		Created:    time.Now().UnixNano(),
		FreshUntil: tw.freshUntil.UnixNano(),
		Size:       tw.written,
	}
	if err := tw.ew.Commit(meta); err == nil {
		tw.c.setPrimaryVary(tw.primaryHex, tw.vary)
	}
	tw.ew = nil
}

// cleanup aborts an uncommitted entry. Deferred on the leader path so a panic in
// the upstream handler (before finish) doesn't leak the temp file.
func (tw *teeWriter) cleanup() {
	if tw.ew != nil {
		tw.ew.Abort()
		tw.ew = nil
	}
}

// Flush forwards to the underlying writer so streaming responses still flush.
func (tw *teeWriter) Flush() {
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
