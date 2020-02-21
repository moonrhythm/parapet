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

func BenchmarkCompress(b *testing.B) {
	data := make([]byte, 5000)
	rand.Read(data)

	h := compress.Gzip().ServeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Write(data)
	}))

	for i := 0; i < b.N; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		r.Header.Set("Accept-Encoding", "gzip, br")
		h.ServeHTTP(w, r)
	}
}
