package headers

import "net/http"

// Header type
type Header struct {
	Key   string
	Value string
}

func buildHeaders(pairs []string) []Header {
	var hs []Header
	for i := 1; i < len(pairs); i += 2 {
		hs = append(hs, Header{
			Key:   pairs[i-1],
			Value: pairs[i],
		})
	}
	return hs
}

// buildCanonicalHeaders is buildHeaders with the key canonicalized once at
// construction. Per-request paths can then use direct map operations and skip
// CanonicalMIMEHeaderKey on every call.
//
// Crucially, the per-request path must still allocate a fresh []string{value}
// for each Set: other middleware (e.g. MapRequest) mutate header value slices
// in place, so any pre-built slice would leak mutations across requests.
func buildCanonicalHeaders(pairs []string) []Header {
	hs := buildHeaders(pairs)
	for i := range hs {
		hs[i].Key = http.CanonicalHeaderKey(hs[i].Key)
	}
	return hs
}

// canonKeys returns canonicalized copies of the input keys for use with
// direct map operations like delete(h, key).
func canonKeys(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = http.CanonicalHeaderKey(k)
	}
	return out
}
