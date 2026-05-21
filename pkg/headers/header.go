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

// canonHeader carries a pre-canonicalized header key and a pre-built
// single-value slice so the per-request path skips both
// CanonicalMIMEHeaderKey and the slice allocation in http.Header.Set.
type canonHeader struct {
	Key   string
	Value []string
}

func buildCanonHeaders(pairs []string) []canonHeader {
	var hs []canonHeader
	for i := 1; i < len(pairs); i += 2 {
		hs = append(hs, canonHeader{
			Key:   http.CanonicalHeaderKey(pairs[i-1]),
			Value: []string{pairs[i]},
		})
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
