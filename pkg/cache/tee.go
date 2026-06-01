package cache

import (
	"bytes"
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
// streams the response to the client AND, when cacheable, buffers the body
// (bounded by the per-object cap). On finish it hands a COMPLETE body to the
// storage backend iff the body is complete (Content-Length matched, or HEAD),
// guaranteeing a truncated response is never stored. Because nothing is persisted
// until finish, an upstream panic before finish leaves no temp/partial state.
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
	caching     bool

	buf        bytes.Buffer
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
		tw.storeKey = variantHashFor(tw.primaryHex, vary, tw.r.Header)
		tw.caching = true
		tw.vary = vary
		tw.freshUntil = dec.freshUntil
		tw.metaHeader = sanitizeHeader(h)
		if cl, ok := contentLength(h); ok {
			tw.hasCL = true
			tw.contentLen = cl
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
	if tw.caching {
		if tw.written+int64(len(p)) > tw.c.maxFileSize {
			tw.abort() // oversize -> stop caching; client still gets the full response
		} else {
			tw.buf.Write(p)
			tw.written += int64(len(p))
		}
	}
	return n, err
}

// abort stops caching this response and drops the buffered body.
func (tw *teeWriter) abort() {
	tw.caching = false
	tw.buf.Reset()
}

// finish stores the entry iff the body is complete; a truncated body (written !=
// Content-Length) or an abort drops it. Runs after the upstream handler returns
// (the client already has the full response) and before the caller closes the
// fill lock, so waiting followers find the committed entry.
func (tw *teeWriter) finish() {
	if !tw.caching {
		return
	}
	complete := tw.method == http.MethodHead || (tw.hasCL && tw.written == tw.contentLen)
	if !complete {
		return
	}
	body := append([]byte(nil), tw.buf.Bytes()...)
	m := Meta{
		Status:     tw.status,
		Header:     tw.metaHeader,
		PrimaryHex: tw.primaryHex,
		Vary:       tw.vary,
		Created:    time.Now().UnixNano(),
		FreshUntil: tw.freshUntil.UnixNano(),
		Size:       int64(len(body)),
	}
	tw.c.storage.Set(tw.storeKey, m, body)
	tw.c.setPrimaryVary(tw.primaryHex, tw.vary)
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
