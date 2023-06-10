package compress

import (
	"compress/gzip"
	"io"
)

// Gzip creates new gzip compress middleware
func Gzip() *Compress {
	return GzipWithLevel(gzip.DefaultCompression)
}

func GzipWithLevel(level int) *Compress {
	return &Compress{
		New: func() Compressor {
			g, err := gzip.NewWriterLevel(io.Discard, level)
			if err != nil {
				panic(err)
			}
			return g
		},
		Encoding:  "gzip",
		Vary:      defaultCompressVary,
		Types:     defaultCompressTypes,
		MinLength: defaultCompressMinLength,
	}
}
