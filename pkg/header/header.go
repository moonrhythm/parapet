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

func Add(h http.Header, key, value string) {
	if h == nil {
		return
	}
	h[key] = append(h[key], value)
}
