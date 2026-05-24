package header

import (
	"net/http"
)

func AddIfNotExists(h http.Header, key, value string) {
	for _, v := range h[key] {
		if v == value {
			return
		}
	}
	h[key] = append(h[key], value)
}

func Get(h http.Header, key string) string {
	if h == nil {
		return ""
	}
	v := h[key]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func Exists(h http.Header, key string) bool {
	if h == nil {
		return false
	}
	v := h[key]
	if len(v) == 0 {
		return false
	}
	return v[0] != ""
}

func Del(h http.Header, key string) {
	if h == nil {
		return
	}
	delete(h, key)
}

func Set(h http.Header, key, value string) {
	if h == nil {
		return
	}
	h[key] = []string{value}
}

// SetShared assigns a pre-built value slice directly into the header map,
// sharing its backing array across every call instead of allocating a fresh
// []string{value} per request the way Set does.
//
// Use it only for values fixed at construction time, and only on response
// headers: the shared slice must be treated as immutable. parapet's own
// middleware never mutate response header value slices in place — MapResponse
// rebuilds the slice, and only MapRequest mutates in place (request headers
// only) — so sharing is safe response-side. The cors middleware shares its
// precomputed header slices the same way.
func SetShared(h http.Header, key string, vs []string) {
	if h == nil {
		return
	}
	h[key] = vs
}

func Add(h http.Header, key, value string) {
	if h == nil {
		return
	}
	h[key] = append(h[key], value)
}
