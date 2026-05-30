package compress_test

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"

	"github.com/moonrhythm/parapet/pkg/compress"
)

func TestCompress(t *testing.T) {
	t.Parallel()

	smallData := []byte("data")
	largeData := make([]byte, 5000)
	rand.Read(largeData)

	makeCompressHandler := func(data []byte) http.Handler {
		return compress.Gzip().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Write(data)
		}))
	}

	smallDataHandler := makeCompressHandler(smallData)
	largeDataHandler := makeCompressHandler(largeData)

	t.Run("Client not support compress", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		largeDataHandler.ServeHTTP(w, r)
		assert.EqualValues(t, largeData, w.Body.Bytes())
	})

	t.Run("Response too small", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		r.Header.Set("Accept-Encoding", "gzip, br")
		smallDataHandler.ServeHTTP(w, r)
		assert.EqualValues(t, smallData, w.Body.Bytes())
	})

	t.Run("Must compress", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		r.Header.Set("Accept-Encoding", "gzip, br")
		largeDataHandler.ServeHTTP(w, r)

		if !assert.Equal(t, "gzip", w.Header().Get("Content-Encoding")) {
			return
		}

		ww, _ := gzip.NewReader(w.Body)
		var decompress bytes.Buffer
		io.Copy(&decompress, ww)
		assert.EqualValues(t, largeData, decompress.Bytes())
	})
}

func TestZstd(t *testing.T) {
	t.Parallel()

	smallData := []byte("data")
	largeData := make([]byte, 5000)
	rand.Read(largeData)

	makeCompressHandler := func(data []byte) http.Handler {
		return compress.Zstd().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Write(data)
		}))
	}

	smallDataHandler := makeCompressHandler(smallData)
	largeDataHandler := makeCompressHandler(largeData)

	t.Run("Client not support compress", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		largeDataHandler.ServeHTTP(w, r)
		assert.EqualValues(t, largeData, w.Body.Bytes())
	})

	t.Run("Response too small", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		r.Header.Set("Accept-Encoding", "gzip, br, zstd")
		smallDataHandler.ServeHTTP(w, r)
		assert.EqualValues(t, smallData, w.Body.Bytes())
	})

	t.Run("Must compress", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		r.Header.Set("Accept-Encoding", "gzip, br, zstd")
		largeDataHandler.ServeHTTP(w, r)

		if !assert.Equal(t, "zstd", w.Header().Get("Content-Encoding")) {
			return
		}

		ww, _ := zstd.NewReader(w.Body)
		defer ww.Close()
		var decompress bytes.Buffer
		io.Copy(&decompress, ww)
		assert.EqualValues(t, largeData, decompress.Bytes())
	})
}

// benchmarkCompress drives requests through the given compress middleware,
// reporting throughput and the achieved compression ratio.
func benchmarkCompress(b *testing.B, c *compress.Compress, data []byte) {
	b.Helper()

	h := c.ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Write(data)
	}))

	var compressedLen int
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Encoding", c.Encoding)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		compressedLen = w.Body.Len()
	}
	b.StopTimer()

	if compressedLen > 0 {
		b.ReportMetric(float64(len(data))/float64(compressedLen), "ratio")
	}
}

func BenchmarkCompress(b *testing.B) {
	data := make([]byte, 5000)
	rand.Read(data)

	// random data barely compresses; also exercise repetitive text which is
	// closer to real-world responses and shows the encoders' ratio differences.
	text := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 256)

	cases := []struct {
		name string
		new  func() *compress.Compress
	}{
		{"Gzip", compress.Gzip},
		{"Deflate", compress.Deflate},
		{"Zstd", compress.Zstd},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.Run("Random", func(b *testing.B) { benchmarkCompress(b, c.new(), data) })
			b.Run("Text", func(b *testing.B) { benchmarkCompress(b, c.new(), text) })
		})
	}
}
