//go:build cbrotli

package compress_test

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/moonrhythm/parapet/pkg/compress"
)

func BenchmarkCompressBr(b *testing.B) {
	data := make([]byte, 5000)
	rand.Read(data)

	text := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 256)

	b.Run("Random", func(b *testing.B) { benchmarkCompress(b, compress.Br(), data) })
	b.Run("Text", func(b *testing.B) { benchmarkCompress(b, compress.Br(), text) })
}
